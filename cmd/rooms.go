package cmd

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	"github.com/teal-bauer/chatto-cli/api"
	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
)

var roomsAll bool

var roomsCmd = &cobra.Command{
	Use:   "rooms",
	Short: "List rooms on the server",
	Long: `Lists rooms on the server. By default only rooms you have joined are
shown; pass --all to see every room visible to you, including ones you
haven't joined.`,
	Args: cobra.NoArgs,
	RunE: runRooms,
}

func init() {
	roomsCmd.Flags().BoolVar(&roomsAll, "all", false, "show all rooms visible to you, including ones you haven't joined")
	rootCmd.AddCommand(roomsCmd)
}

func runRooms(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	rooms, err := c.ListRooms(ctx)
	if err != nil {
		return err
	}

	if !roomsAll {
		filtered := rooms[:0]
		for _, r := range rooms {
			if r.GetViewerState().GetIsMember() {
				filtered = append(filtered, r)
			}
		}
		rooms = filtered
	}

	if flagJSON {
		printProtoJSONList(rooms)
		return nil
	}

	if len(rooms) == 0 {
		fmt.Println("No rooms.")
		return nil
	}

	w := tw()
	fmt.Fprintln(w, bold("ROOM")+"\t"+bold("ID")+"\t"+bold("KIND")+"\t"+bold("JOINED"))
	for _, rws := range rooms {
		room := rws.GetRoom()
		joined := ""
		if rws.GetViewerState().GetIsMember() {
			joined = green("✓")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", roomLabel(room), dim(room.GetId()), roomKindLabel(room.GetKind()), joined)
	}
	w.Flush()
	return nil
}

// roomLabel formats a room's display name for table output: "#name" for
// channels, the bare name for DMs (which aren't prefixed with #).
func roomLabel(room *apiv1.Room) string {
	if room.GetKind() == apiv1.RoomKind_ROOM_KIND_DM {
		return room.GetName()
	}
	return "#" + room.GetName()
}

func roomKindLabel(kind apiv1.RoomKind) string {
	switch kind {
	case apiv1.RoomKind_ROOM_KIND_DM:
		return "dm"
	case apiv1.RoomKind_ROOM_KIND_CHANNEL:
		return "channel"
	default:
		return "unknown"
	}
}

var joinRoomCmd = &cobra.Command{
	Use:   "join <room>",
	Short: "Join a room",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		roomID, err := resolveRoom(ctx, c, args[0])
		if err != nil {
			return err
		}
		if _, err := c.JoinRoom(ctx, roomID); err != nil {
			return err
		}
		fmt.Printf("Joined %s.\n", args[0])
		return nil
	},
}

var leaveRoomCmd = &cobra.Command{
	Use:   "leave <room>",
	Short: "Leave a room",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		roomID, err := resolveRoom(ctx, c, args[0])
		if err != nil {
			return err
		}
		if _, err := c.LeaveRoom(ctx, roomID); err != nil {
			return leaveRoomErr(err)
		}
		fmt.Printf("Left %s.\n", args[0])
		return nil
	},
}

// leaveRoomErr rewrites the FailedPrecondition ChattoError that LeaveRoom
// returns for DM/universal rooms (which can't be left) into a clearer
// message, rather than surfacing the raw RPC error.
func leaveRoomErr(err error) error {
	var ce *api.ChattoError
	if errors.As(err, &ce) && ce.Code == connect.CodeFailedPrecondition {
		return fmt.Errorf("can't leave this room: %s", ce.Message)
	}
	return err
}

func init() {
	rootCmd.AddCommand(joinRoomCmd)
	rootCmd.AddCommand(leaveRoomCmd)
}
