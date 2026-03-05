package cmd

import (
	"errors"
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/teal-bauer/chatto-cli/api"
	"github.com/teal-bauer/chatto-cli/config"
)

var errExit = errors.New("exit")

var replCmd = &cobra.Command{
	Use:   "repl",
	Short: "Start an interactive chatto shell",
	Long: `Starts an interactive shell where you can run chatto commands
without the 'chatto' prefix. Supports setting a default space/room context.

Type 'help' for available commands, 'exit' or Ctrl+D to quit.`,
	RunE: runREPL,
}

func init() {
	rootCmd.AddCommand(replCmd)
}

// replState holds the interactive session state.
type replState struct {
	client       *api.Client
	profile      string
	instance     string
	defaultSpace string // ID
	defaultRoom  string // ID
	spaceName    string // human-readable
	roomName     string // human-readable
	watchCancel  context.CancelFunc
	cache        *EventCache
}

func runREPL(cmd *cobra.Command, args []string) error {
	prof, name, err := config.GetProfile(flagProfile, flagInstance)
	if err != nil {
		return err
	}

	client := api.New(prof.Instance, prof.Session)
	state := &replState{
		client:   client,
		profile:  name,
		instance: prof.Instance,
		cache:    NewEventCache(client),
	}

	fmt.Printf("chatto shell — %s (profile: %s)\n", prof.Instance, name)
	fmt.Println(`Type "help" for commands, "exit" to quit.`)
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(state.prompt())
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := state.dispatch(line); err != nil {
			if errors.Is(err, errExit) {
				break
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if state.watchCancel != nil {
		state.watchCancel()
	}
	fmt.Println("\nBye.")
	return nil
}

func (s *replState) prompt() string {
	parts := []string{"chatto"}
	if s.spaceName != "" {
		parts = append(parts, s.spaceName)
	}
	if s.roomName != "" {
		parts = append(parts, "#"+s.roomName)
	}
	return cyan(strings.Join(parts, ":")) + " > "
}

func (s *replState) dispatch(line string) error {
	fields := splitLine(line)
	if len(fields) == 0 {
		return nil
	}
	verb := strings.ToLower(fields[0])
	rest := fields[1:]

	switch verb {
	case "exit", "quit", "q":
		return errExit

	case "help", "?":
		s.printHelp()

	case "profile":
		return s.cmdProfile(rest)

	case "spaces", "ls":
		return s.cmdSpaces()

	case "rooms":
		return s.cmdRooms(rest)

	case "use":
		return s.cmdUse(rest)

	case "join":
		return s.cmdJoin(rest)

	case "leave":
		return s.cmdLeave(rest)

	case "messages", "msgs", "history":
		return s.cmdMessages(rest)

	case "send", "say":
		return s.cmdSend(rest)

	case "watch":
		return s.cmdWatch(rest)

	case "unwatch":
		return s.cmdUnwatch()

	case "me", "whoami":
		return s.cmdMe()

	default:
		// If a default room is set, treat input as a message to send
		if s.defaultSpace != "" && s.defaultRoom != "" {
			return s.sendMessage(s.defaultSpace, s.defaultRoom, line)
		}
		fmt.Printf("Unknown command: %q. Type 'help' for help.\n", verb)
	}
	return nil
}

func (s *replState) cmdProfile(args []string) error {
	if len(args) == 0 {
		// Show current
		fmt.Printf("Profile: %s\nInstance: %s\n", s.profile, s.instance)
		return nil
	}
	name := args[0]
	var instanceOverride string
	if len(args) > 1 {
		instanceOverride = args[1]
	}
	prof, _, err := config.GetProfile(name, instanceOverride)
	if err != nil {
		return err
	}
	s.client = api.New(prof.Instance, prof.Session)
	s.profile = name
	s.instance = prof.Instance
	s.defaultSpace = ""
	s.defaultRoom = ""
	s.spaceName = ""
	s.roomName = ""
	fmt.Printf("Switched to profile %q (%s)\n", name, prof.Instance)
	return nil
}

func (s *replState) cmdSpaces() error {
	spaces, err := s.client.GetSpaces()
	if err != nil {
		return err
	}
	if len(spaces) == 0 {
		fmt.Println("No spaces.")
		return nil
	}
	w := tw()
	fmt.Fprintln(w, bold("NAME")+"\t"+bold("ID")+"\t"+bold("MEMBER"))
	for _, sp := range spaces {
		member := ""
		if sp.ViewerIsMember {
			member = green("✓")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", sp.Name, dim(sp.ID), member)
	}
	w.Flush()
	return nil
}

func (s *replState) cmdRooms(args []string) error {
	spaceID := s.defaultSpace
	if len(args) > 0 {
		var err error
		spaceID, err = s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
	}
	if spaceID == "" {
		return fmt.Errorf("no space set; use 'use <space>' first or pass a space argument")
	}
	rooms, err := s.client.GetRooms(spaceID)
	if err != nil {
		return err
	}
	if len(rooms) == 0 {
		fmt.Println("No rooms.")
		return nil
	}
	w := tw()
	fmt.Fprintln(w, bold("ROOM")+"\t"+bold("ID")+"\t"+bold("JOINED"))
	for _, r := range rooms {
		joined := ""
		if r.Joined {
			joined = green("✓")
		}
		fmt.Fprintf(w, "#%s\t%s\t%s\n", r.Name, dim(r.ID), joined)
	}
	w.Flush()
	return nil
}

func (s *replState) cmdUse(args []string) error {
	switch len(args) {
	case 0:
		if s.spaceName != "" {
			fmt.Printf("Space: %s (%s)\n", s.spaceName, s.defaultSpace)
		}
		if s.roomName != "" {
			fmt.Printf("Room:  #%s (%s)\n", s.roomName, s.defaultRoom)
		}
		if s.spaceName == "" && s.roomName == "" {
			fmt.Println("No default space/room set.")
		}
	case 1:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		s.defaultSpace = spaceID
		s.spaceName = args[0]
		s.defaultRoom = ""
		s.roomName = ""
		fmt.Printf("Using space %s\n", args[0])
	case 2:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err := s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
		s.defaultSpace = spaceID
		s.spaceName = args[0]
		s.defaultRoom = roomID
		s.roomName = strings.TrimPrefix(args[1], "#")
		fmt.Printf("Using %s / #%s\n", args[0], s.roomName)
	}
	return nil
}

func (s *replState) cmdJoin(args []string) error {
	switch len(args) {
	case 0:
		return fmt.Errorf("usage: join <space> [room]")
	case 1:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		if err := s.client.JoinSpace(spaceID); err != nil {
			return err
		}
		fmt.Printf("Joined space %s\n", args[0])
	default:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err := s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
		if err := s.client.JoinRoom(spaceID, roomID); err != nil {
			return err
		}
		fmt.Printf("Joined #%s\n", args[1])
	}
	return nil
}

func (s *replState) cmdLeave(args []string) error {
	switch len(args) {
	case 0:
		return fmt.Errorf("usage: leave <space> [room]")
	case 1:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		if err := s.client.LeaveSpace(spaceID); err != nil {
			return err
		}
		fmt.Printf("Left space %s\n", args[0])
	default:
		spaceID, err := s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err := s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
		if err := s.client.LeaveRoom(spaceID, roomID); err != nil {
			return err
		}
		fmt.Printf("Left #%s\n", args[1])
	}
	return nil
}

func (s *replState) cmdMessages(args []string) error {
	spaceID := s.defaultSpace
	roomID := s.defaultRoom
	limit := 20

	switch len(args) {
	case 0:
		// use defaults
	case 1:
		// could be a number (limit) or a room name
		if n := parseInt(args[0]); n > 0 {
			limit = n
		} else if s.defaultSpace != "" {
			var err error
			roomID, err = s.client.ResolveRoomID(s.defaultSpace, args[0])
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("usage: messages [space] [room] [limit]")
		}
	case 2:
		var err error
		spaceID, err = s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err = s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
	case 3:
		var err error
		spaceID, err = s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err = s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
		if n := parseInt(args[2]); n > 0 {
			limit = n
		}
	}

	if spaceID == "" || roomID == "" {
		return fmt.Errorf("no space/room context; use 'use <space> <room>' first")
	}

	events, err := s.client.GetRoomEvents(spaceID, roomID, limit)
	if err != nil {
		return err
	}
	s.cache.StoreEvents(events)
	printEvents(events, s.client.Instance(), nil, s.cache)
	return nil
}

func (s *replState) cmdSend(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: send [space room] <message...>")
	}

	spaceID := s.defaultSpace
	roomID := s.defaultRoom
	var msgArgs []string

	if spaceID == "" {
		if len(args) < 3 {
			return fmt.Errorf("no default space/room; usage: send <space> <room> <message>")
		}
		var err error
		spaceID, err = s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
		roomID, err = s.client.ResolveRoomID(spaceID, args[1])
		if err != nil {
			return err
		}
		msgArgs = args[2:]
	} else if roomID == "" {
		if len(args) < 2 {
			return fmt.Errorf("no default room; usage: send <room> <message>")
		}
		var err error
		roomID, err = s.client.ResolveRoomID(spaceID, args[0])
		if err != nil {
			return err
		}
		msgArgs = args[1:]
	} else {
		msgArgs = args
	}

	return s.sendMessage(spaceID, roomID, strings.Join(msgArgs, " "))
}

func (s *replState) sendMessage(spaceID, roomID, body string) error {
	ev, err := s.client.PostMessage(spaceID, roomID, body)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", dim("✓ sent "+ev.ID))
	return nil
}

func (s *replState) cmdWatch(args []string) error {
	if s.watchCancel != nil {
		fmt.Println("Already watching. Use 'unwatch' to stop.")
		return nil
	}

	spaceID := s.defaultSpace
	if len(args) > 0 {
		var err error
		spaceID, err = s.client.ResolveSpaceID(args[0])
		if err != nil {
			return err
		}
	}
	if spaceID == "" {
		return fmt.Errorf("no space set; use 'use <space>' or pass a space argument")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel

	ch, err := s.client.Watch(ctx, spaceID)
	if err != nil {
		cancel()
		s.watchCancel = nil
		return err
	}

	fmt.Printf("%s\n", dim("Watching space "+spaceID+" in background. Use 'unwatch' to stop."))
	go func() {
		for ev := range ch {
			if ev.Err != nil {
				fmt.Fprintf(os.Stderr, "\nwatch: %v\n", ev.Err)
				continue
			}
			fmt.Print("\n")
			printEvents([]api.SpaceEvent{ev.SpaceEvent}, s.client.Instance(), nil, s.cache)
			fmt.Print(s.prompt())
		}
	}()
	return nil
}

func (s *replState) cmdUnwatch() error {
	if s.watchCancel == nil {
		fmt.Println("Not watching.")
		return nil
	}
	s.watchCancel()
	s.watchCancel = nil
	fmt.Println("Stopped watching.")
	return nil
}

func (s *replState) cmdMe() error {
	me, err := s.client.Me()
	if err != nil {
		return err
	}
	if me == nil {
		return fmt.Errorf("not authenticated")
	}
	w := tw()
	fmt.Fprintf(w, "Login:\t%s\n", me.Login)
	fmt.Fprintf(w, "Display name:\t%s\n", me.DisplayName)
	fmt.Fprintf(w, "ID:\t%s\n", dim(me.ID))
	fmt.Fprintf(w, "Presence:\t%s\n", me.PresenceStatus)
	w.Flush()
	return nil
}

func (s *replState) printHelp() {
	help := [][2]string{
		{"spaces / ls", "List spaces"},
		{"rooms [space]", "List rooms in a space"},
		{"use <space> [room]", "Set default space/room context"},
		{"join <space> [room]", "Join a space or room"},
		{"leave <space> [room]", "Leave a space or room"},
		{"messages [space room] [n]", "Show recent messages"},
		{"send [space room] <msg>", "Send a message"},
		{"watch [space]", "Stream live events in background"},
		{"unwatch", "Stop live event stream"},
		{"me / whoami", "Show current user"},
		{"profile [name] [url]", "Show or switch profile"},
		{"exit / quit", "Exit the shell"},
	}
	w := tw()
	for _, h := range help {
		fmt.Fprintf(w, "  %s\t%s\n", cyan(h[0]), h[1])
	}
	w.Flush()
	fmt.Println()
	fmt.Println(dim("When a default room is set, any unrecognized input is sent as a message."))
}

// splitLine splits a line on whitespace, respecting single- and double-quoted strings.
func splitLine(line string) []string {
	var parts []string
	var current strings.Builder
	var quoteChar rune
	inQuote := false
	for _, r := range line {
		switch {
		case (r == '\'' || r == '"') && !inQuote:
			inQuote = true
			quoteChar = r
		case inQuote && r == quoteChar:
			inQuote = false
		case (r == ' ' || r == '\t') && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func parseInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
