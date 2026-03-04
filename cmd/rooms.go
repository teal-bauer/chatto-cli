package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var roomsCmd = &cobra.Command{
	Use:   "rooms <space>",
	Short: "List rooms in a space",
	Args:  cobra.ExactArgs(1),
	RunE:  runRooms,
}

func init() {
	rootCmd.AddCommand(roomsCmd)
}

func runRooms(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	spaceID, err := resolveSpace(c, args[0])
	if err != nil {
		return err
	}

	rooms, err := c.GetRooms(spaceID)
	if err != nil {
		return err
	}

	if flagJSON {
		printJSON(rooms)
		return nil
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

var joinRoomCmd = &cobra.Command{
	Use:   "join <space> <room>",
	Short: "Join a room",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		spaceID, err := resolveSpace(c, args[0])
		if err != nil {
			return err
		}
		roomID, err := resolveRoom(c, spaceID, args[1])
		if err != nil {
			return err
		}
		if err := c.JoinRoom(spaceID, roomID); err != nil {
			return err
		}
		fmt.Printf("Joined #%s.\n", args[1])
		return nil
	},
}

var leaveRoomCmd = &cobra.Command{
	Use:   "leave <space> <room>",
	Short: "Leave a room",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		spaceID, err := resolveSpace(c, args[0])
		if err != nil {
			return err
		}
		roomID, err := resolveRoom(c, spaceID, args[1])
		if err != nil {
			return err
		}
		if err := c.LeaveRoom(spaceID, roomID); err != nil {
			return err
		}
		fmt.Printf("Left #%s.\n", args[1])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(joinRoomCmd)
	rootCmd.AddCommand(leaveRoomCmd)
}
