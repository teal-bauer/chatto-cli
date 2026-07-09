package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/teal-bauer/chatto-cli/api"
	realtimev1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/realtime/v1"
)

var (
	watchRoom    string
	watchHistory int
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stream live events from the server",
	Long: `Subscribes to the server's realtime event stream and prints events as
they arrive. Useful for scripting: pipe the output or use --json for a
machine-readable stream.

Press Ctrl+C to stop.`,
	Args: cobra.NoArgs,
	RunE: runWatch,
}

func init() {
	watchCmd.Flags().StringVar(&watchRoom, "room", "", "filter events to this room (ID or name)")
	watchCmd.Flags().IntVar(&watchHistory, "history", 0, "show last N messages from --room before streaming (ignored when --json is set)")
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(cmd.Context())
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

	filterRoom := ""
	if watchRoom != "" {
		filterRoom, err = resolveRoom(ctx, c, watchRoom)
		if err != nil {
			return err
		}
	}

	// Pre-fetch room labels for display.
	rooms, err := c.ListRooms(ctx)
	if err != nil {
		return fmt.Errorf("fetching rooms: %w", err)
	}
	roomNames := make(map[string]string, len(rooms))
	for _, rws := range rooms {
		roomNames[rws.GetRoom().GetId()] = roomLabel(rws.GetRoom())
	}

	renderer := newEventRenderer(ctx, c, roomNames)

	// Show history before streaming.
	if watchHistory > 0 && !flagJSON {
		if filterRoom == "" {
			fmt.Fprintln(os.Stderr, "warn: --history requires --room")
		} else {
			page, err := c.GetRoomEvents(ctx, filterRoom, int32(watchHistory), "", "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: fetching history: %v\n", err)
			} else {
				for _, ev := range page.GetEvents() {
					renderer.renderTimelineEvent(filterRoom, ev)
				}
				fmt.Fprintln(os.Stderr, "--- live ---")
			}
		}
	}

	fmt.Fprintln(os.Stderr, "Watching server (Ctrl+C to stop)...")

	ch, err := c.Watch(ctx)
	if err != nil {
		return err
	}

	for wev := range ch {
		if wev.Err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if terr := watchTerminalErr(wev.Err); terr != nil {
				return terr
			}
			fmt.Fprintf(os.Stderr, "warn: %v\n", wev.Err)
			continue
		}

		roomID := envelopeRoomID(wev.Event.Envelope)
		if filterRoom != "" && roomID != "" && roomID != filterRoom {
			continue
		}

		if flagJSON {
			printProtoJSON(wev.Event.Envelope)
			continue
		}

		renderer.renderHydrated(wev.Event)
	}

	return nil
}

// watchTerminalErr maps a WatchEvent.Err delivered on Client.Watch's channel
// to the error runWatch should return (which cobra surfaces as a non-zero
// exit), or nil if err isn't terminal and streaming should just log a
// warning and keep going. Per Client.Watch's contract, a terminal error
// (ErrUnauthenticated or ErrRealtimeStopped) is always the last value sent
// before the channel closes, so returning it here ends runWatch's range loop
// naturally rather than looping forever on a channel that's already closed.
func watchTerminalErr(err error) error {
	var unauth *api.ErrUnauthenticated
	if errors.As(err, &unauth) {
		return fmt.Errorf("%w (run `chatto login` to re-authenticate)", err)
	}
	var stopped *api.ErrRealtimeStopped
	if errors.As(err, &stopped) {
		return err
	}
	return nil
}

// envelopeRoomID extracts the room ID from whichever event payload is set on
// env's `event` oneof, or "" for event kinds with no room_id field
// (server_updated, session_terminated and the like) or where it's unset.
//
// This is done via protoreflect rather than an explicit case per oneof
// variant: realtime.proto carries room_id on a couple dozen event kinds
// (some via a plain `string room_id`, some via `optional string room_id`),
// and a hardcoded switch silently stops filtering newly added room-bearing
// event kinds correctly (they'd fall through to "", which --room's filter
// treats as "unscoped" and lets through unfiltered). Reflection keeps this
// correct as realtime.proto grows.
func envelopeRoomID(env *realtimev1.RealtimeEventEnvelope) string {
	if env == nil {
		return ""
	}
	refl := env.ProtoReflect()
	oneofDesc := refl.Descriptor().Oneofs().ByName("event")
	if oneofDesc == nil {
		return ""
	}
	fd := refl.WhichOneof(oneofDesc)
	if fd == nil {
		return ""
	}
	payload := refl.Get(fd).Message()
	if !payload.IsValid() {
		return ""
	}
	roomIDField := payload.Descriptor().Fields().ByName("room_id")
	if roomIDField == nil || roomIDField.Kind() != protoreflect.StringKind {
		return ""
	}
	return payload.Get(roomIDField).String()
}

// renderHydrated renders one hydrated realtime event (from Client.Watch).
func (r *eventRenderer) renderHydrated(hv *api.HydratedEvent) {
	env := hv.Envelope
	ts := formatTimestamp(env.GetCreatedAt())
	roomID := envelopeRoomID(env)
	actor := displayName(hv.Actor, env.GetActorId())

	switch hv.EventName {
	case "message_posted":
		r.renderMessage(roomID, ts, env.GetActorId(), hv.Message)
	case "message_updated":
		if hv.Message != nil {
			printMsg(ts, r.roomLabel(roomID), "", "<"+actor+">", "[edit] "+renderBody(hv.Message, r.instance), nil, nil)
		}
	case "message_deleted":
		printStatus(ts, r.roomLabel(roomID), "*** message deleted")
	case "reaction_added":
		msg := "*** " + actor + " reacted"
		if emoji := env.GetReactionAdded().GetEmoji(); emoji != "" {
			msg += " " + emoji
		}
		if orig := r.refs.lookup(r.ctx, roomID, env.GetReactionAdded().GetMessageEventId()); orig != nil {
			msg += dim(" → " + r.formatRef(orig))
		}
		printStatus(ts, r.roomLabel(roomID), msg)
	case "reaction_removed":
		msg := "*** " + actor + " removed reaction"
		if emoji := env.GetReactionRemoved().GetEmoji(); emoji != "" {
			msg += " " + emoji
		}
		if orig := r.refs.lookup(r.ctx, roomID, env.GetReactionRemoved().GetMessageEventId()); orig != nil {
			msg += dim(" → " + r.formatRef(orig))
		}
		printStatus(ts, r.roomLabel(roomID), msg)
	case "user_joined_room":
		printStatus(ts, r.roomLabel(roomID), "*** "+actor+" has joined")
	case "user_left_room":
		printStatus(ts, r.roomLabel(roomID), "*** "+actor+" has left")
	case "room_created":
		printStatus(ts, r.roomLabel(roomID), "*** room created")
	case "room_updated":
		printStatus(ts, r.roomLabel(roomID), "*** room updated")
	case "room_deleted":
		printStatus(ts, r.roomLabel(roomID), "*** room deleted")
	case "room_archived":
		printStatus(ts, r.roomLabel(roomID), "*** room archived")
	case "room_unarchived":
		printStatus(ts, r.roomLabel(roomID), "*** room unarchived")
	default:
		// Other event kinds (typing, presence, notifications, calls,
		// server/profile updates, ...) aren't rendered as chat lines;
		// --debug below still shows them.
	}

	if flagDebug {
		printProtoJSON(env)
	}
}
