package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var spacesCmd = &cobra.Command{
	Use:   "spaces",
	Short: "List spaces",
	RunE:  runSpaces,
}

func init() {
	rootCmd.AddCommand(spacesCmd)
}

func runSpaces(cmd *cobra.Command, args []string) error {
	c, err := clientFromFlags()
	if err != nil {
		return err
	}

	spaces, err := c.GetSpaces()
	if err != nil {
		return err
	}

	if flagJSON {
		printJSON(spaces)
		return nil
	}

	if len(spaces) == 0 {
		fmt.Println("No spaces.")
		return nil
	}

	w := tw()
	fmt.Fprintln(w, bold("NAME")+"\t"+bold("ID")+"\t"+bold("MEMBER"))
	for _, s := range spaces {
		member := ""
		if s.ViewerIsMember {
			member = green("✓")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, dim(s.ID), member)
	}
	w.Flush()
	return nil
}

var joinSpaceCmd = &cobra.Command{
	Use:   "join-space <space>",
	Short: "Join a space",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		spaceID, err := resolveSpace(c, args[0])
		if err != nil {
			return err
		}
		if err := c.JoinSpace(spaceID); err != nil {
			return err
		}
		fmt.Printf("Joined space %s.\n", args[0])
		return nil
	},
}

var leaveSpaceCmd = &cobra.Command{
	Use:   "leave-space <space>",
	Short: "Leave a space",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		spaceID, err := resolveSpace(c, args[0])
		if err != nil {
			return err
		}
		if err := c.LeaveSpace(spaceID); err != nil {
			return err
		}
		fmt.Printf("Left space %s.\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(joinSpaceCmd)
	rootCmd.AddCommand(leaveSpaceCmd)
}
