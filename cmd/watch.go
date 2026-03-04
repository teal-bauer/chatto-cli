package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/teal-bauer/chatto-cli/api"
)

var watchCmd = &cobra.Command{
	Use:   "watch <space>",
	Short: "Stream live events from a space",
	Long: `Subscribes to a space and prints events as they arrive.
Useful for scripting: pipe the output or use --json for machine-readable stream.

Press Ctrl+C to stop.`,
	Args: cobra.ExactArgs(1),
	RunE: runWatch,
}

var (
	watchRoom    string
	watchHistory int
)

func init() {
	watchCmd.Flags().StringVar(&watchRoom, "room", "", "filter events to this room (ID or name)")
	watchCmd.Flags().IntVar(&watchHistory, "history", 0, "show last N messages before streaming")
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	spaceID, err := resolveSpace(c, args[0])
	if err != nil {
		return err
	}

	filterRoom := ""
	if watchRoom != "" {
		filterRoom, err = resolveRoom(c, spaceID, watchRoom)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Pre-fetch room names for display.
	rooms, err := c.GetRooms(spaceID)
	if err != nil {
		return fmt.Errorf("fetching rooms: %w", err)
	}
	roomNames := make(map[string]string, len(rooms))
	for _, r := range rooms {
		roomNames[r.ID] = r.Name
	}

	cache := NewEventCache(c)

	// Show history before streaming.
	if watchHistory > 0 && !flagJSON {
		historyRoom := filterRoom
		if historyRoom == "" && len(rooms) == 1 {
			historyRoom = rooms[0].ID
		}
		if historyRoom != "" {
			events, err := c.GetRoomEvents(spaceID, historyRoom, watchHistory)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: fetching history: %v\n", err)
			} else {
				cache.StoreEvents(events)
				printEvents(events, c.Instance(), roomNames, cache)
				fmt.Fprintln(os.Stderr, "--- live ---")
			}
		} else {
			fmt.Fprintf(os.Stderr, "warn: --history requires --room when space has multiple rooms\n")
		}
	}

	fmt.Fprintf(os.Stderr, "Watching space %s (Ctrl+C to stop)...\n", args[0])

	ch, err := c.Watch(ctx, spaceID)
	if err != nil {
		return err
	}

	for ev := range ch {
		if ev.Err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "warn: %v\n", ev.Err)
			continue
		}
		if filterRoom != "" && ev.SpaceEvent.Event.RoomID != "" {
			if ev.SpaceEvent.Event.RoomID != filterRoom {
				continue
			}
		}

		if flagJSON {
			printJSON(ev.SpaceEvent)
			continue
		}

		printEvents([]api.SpaceEvent{ev.SpaceEvent}, c.Instance(), roomNames, cache)
	}

	return nil
}
