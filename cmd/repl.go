package cmd

import (
	"bufio"
	"context"
	"errors"
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
without the 'chatto' prefix. Supports setting a default room context.

Type 'help' for available commands, 'exit' or Ctrl+D to quit.`,
	RunE: runREPL,
}

func init() {
	rootCmd.AddCommand(replCmd)
}

// replState holds the interactive session state.
type replState struct {
	ctx         context.Context
	client      *api.Client
	profile     string
	instance    string
	defaultRoom string // ID
	roomName    string // human-readable
	watchCancel context.CancelFunc
	renderer    *eventRenderer
}

func runREPL(cmd *cobra.Command, args []string) error {
	prof, name, err := config.GetProfile(flagProfile, flagInstance)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client := api.New(prof.Instance, prof.Token, prof.Session)
	state := &replState{
		ctx:      ctx,
		client:   client,
		profile:  name,
		instance: prof.Instance,
		renderer: newEventRenderer(ctx, client, nil),
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
	if s.roomName != "" {
		return cyan("chatto:#"+s.roomName) + " > "
	}
	return cyan("chatto") + " > "
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

	case "rooms", "ls":
		return s.cmdRooms()

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
		// If a default room is set, treat input as a message to send.
		if s.defaultRoom != "" {
			return s.sendMessage(s.defaultRoom, line)
		}
		fmt.Printf("Unknown command: %q. Type 'help' for help.\n", verb)
	}
	return nil
}

func (s *replState) cmdProfile(args []string) error {
	if len(args) == 0 {
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
	s.client = api.New(prof.Instance, prof.Token, prof.Session)
	s.renderer = newEventRenderer(s.ctx, s.client, nil)
	s.profile = name
	s.instance = prof.Instance
	s.defaultRoom = ""
	s.roomName = ""
	fmt.Printf("Switched to profile %q (%s)\n", name, prof.Instance)
	return nil
}

func (s *replState) cmdRooms() error {
	rooms, err := s.client.ListRooms(s.ctx)
	if err != nil {
		return err
	}
	if len(rooms) == 0 {
		fmt.Println("No rooms.")
		return nil
	}
	w := tw()
	fmt.Fprintln(w, bold("ROOM")+"\t"+bold("ID")+"\t"+bold("JOINED"))
	for _, rws := range rooms {
		room := rws.GetRoom()
		joined := ""
		if rws.GetViewerState().GetIsMember() {
			joined = green("✓")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", roomLabel(room), dim(room.GetId()), joined)
	}
	w.Flush()
	return nil
}

func (s *replState) cmdUse(args []string) error {
	switch len(args) {
	case 0:
		if s.roomName != "" {
			fmt.Printf("Room: #%s (%s)\n", s.roomName, s.defaultRoom)
		} else {
			fmt.Println("No default room set.")
		}
	case 1:
		roomID, err := s.client.ResolveRoomID(s.ctx, args[0])
		if err != nil {
			return err
		}
		s.defaultRoom = roomID
		s.roomName = strings.TrimPrefix(args[0], "#")
		fmt.Printf("Using #%s\n", s.roomName)
	default:
		return fmt.Errorf("usage: use [room]")
	}
	return nil
}

func (s *replState) cmdJoin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: join <room>")
	}
	roomID, err := s.client.ResolveRoomID(s.ctx, args[0])
	if err != nil {
		return err
	}
	if _, err := s.client.JoinRoom(s.ctx, roomID); err != nil {
		return err
	}
	fmt.Printf("Joined %s\n", args[0])
	return nil
}

func (s *replState) cmdLeave(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: leave <room>")
	}
	roomID, err := s.client.ResolveRoomID(s.ctx, args[0])
	if err != nil {
		return err
	}
	if _, err := s.client.LeaveRoom(s.ctx, roomID); err != nil {
		return leaveRoomErr(err)
	}
	fmt.Printf("Left %s\n", args[0])
	return nil
}

func (s *replState) cmdMessages(args []string) error {
	roomID := s.defaultRoom
	limit := 20

	switch len(args) {
	case 0:
		// use defaults
	case 1:
		// could be a number (limit) or a room name
		if n := parseInt(args[0]); n > 0 {
			limit = n
		} else {
			var err error
			roomID, err = s.client.ResolveRoomID(s.ctx, args[0])
			if err != nil {
				return err
			}
		}
	default:
		var err error
		roomID, err = s.client.ResolveRoomID(s.ctx, args[0])
		if err != nil {
			return err
		}
		if n := parseInt(args[1]); n > 0 {
			limit = n
		}
	}

	if roomID == "" {
		return fmt.Errorf("no room context; use 'use <room>' first")
	}

	page, err := s.client.GetRoomEvents(s.ctx, roomID, int32(limit), "", "")
	if err != nil {
		return err
	}
	for _, ev := range page.GetEvents() {
		s.renderer.renderTimelineEvent(roomID, ev)
	}
	return nil
}

func (s *replState) cmdSend(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: send [room] <message...>")
	}

	roomID := s.defaultRoom
	var msgArgs []string

	if roomID == "" {
		if len(args) < 2 {
			return fmt.Errorf("no default room; usage: send <room> <message>")
		}
		var err error
		roomID, err = s.client.ResolveRoomID(s.ctx, args[0])
		if err != nil {
			return err
		}
		msgArgs = args[1:]
	} else {
		msgArgs = args
	}

	return s.sendMessage(roomID, strings.Join(msgArgs, " "))
}

func (s *replState) sendMessage(roomID, body string) error {
	msg, err := s.client.CreateMessage(s.ctx, roomID, body, "", "")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", dim("✓ sent "+msg.GetId()))
	return nil
}

func (s *replState) cmdWatch(args []string) error {
	if s.watchCancel != nil {
		fmt.Println("Already watching. Use 'unwatch' to stop.")
		return nil
	}

	filterRoom := s.defaultRoom
	if len(args) > 0 {
		var err error
		filterRoom, err = s.client.ResolveRoomID(s.ctx, args[0])
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(s.ctx)
	s.watchCancel = cancel

	ch, err := s.client.Watch(ctx)
	if err != nil {
		cancel()
		s.watchCancel = nil
		return err
	}

	label := "the server"
	if filterRoom != "" {
		label = "room " + filterRoom
	}
	fmt.Printf("%s\n", dim("Watching "+label+" in background. Use 'unwatch' to stop."))
	go func() {
		// Whatever ends the channel -- ctx cancellation via unwatch, or a
		// terminal error from Watch -- clear watchCancel so a subsequent
		// 'watch' isn't stuck behind a dead watcher forever saying "Already
		// watching."
		defer func() {
			cancel()
			s.watchCancel = nil
		}()
		var terminalErr error
		for wev := range ch {
			if wev.Err != nil {
				terminalErr = wev.Err
				fmt.Fprintf(os.Stderr, "\nwatch: %v\n", wev.Err)
				continue
			}
			roomID := envelopeRoomID(wev.Event.Envelope)
			if filterRoom != "" && roomID != "" && roomID != filterRoom {
				continue
			}
			fmt.Print("\n")
			s.renderer.renderHydrated(wev.Event)
			fmt.Print(s.prompt())
		}
		if terminalErr != nil {
			fmt.Fprintf(os.Stderr, "\nwatch: stream stopped (%v); use 'watch' to restart\n", terminalErr)
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
	viewer, err := s.client.GetViewer(s.ctx)
	if err != nil {
		return err
	}
	if viewer == nil {
		return fmt.Errorf("not authenticated")
	}
	profile := viewer.GetProfile()
	w := tw()
	fmt.Fprintf(w, "Login:\t%s\n", profile.GetLogin())
	fmt.Fprintf(w, "Display name:\t%s\n", profile.GetDisplayName())
	fmt.Fprintf(w, "ID:\t%s\n", dim(profile.GetId()))
	fmt.Fprintf(w, "Presence:\t%s\n", presenceLabel(profile.GetPresenceStatus()))
	w.Flush()
	return nil
}

func (s *replState) printHelp() {
	help := [][2]string{
		{"rooms / ls", "List rooms"},
		{"use [room]", "Set/show default room context"},
		{"join <room>", "Join a room"},
		{"leave <room>", "Leave a room"},
		{"messages [room] [n]", "Show recent messages"},
		{"send [room] <msg>", "Send a message"},
		{"watch [room]", "Stream live events in background (whole server, or filtered to a room)"},
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
