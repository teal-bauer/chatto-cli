package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"

	"github.com/teal-bauer/chatto-cli/config"
)

var meCmd = &cobra.Command{
	Use:   "me",
	Short: "Show the current authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		viewer, err := c.GetViewer(ctx)
		if err != nil {
			return err
		}
		if viewer == nil {
			return fmt.Errorf("not authenticated")
		}
		if flagJSON {
			printProtoJSON(viewer)
			return nil
		}
		profile := viewer.GetProfile()
		w := tw()
		fmt.Fprintf(w, "Login:\t%s\n", profile.GetLogin())
		fmt.Fprintf(w, "Display name:\t%s\n", profile.GetDisplayName())
		fmt.Fprintf(w, "ID:\t%s\n", dim(profile.GetId()))
		fmt.Fprintf(w, "Presence:\t%s\n", presenceLabel(profile.GetPresenceStatus()))
		w.Flush()
		return nil
	},
}

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List saved profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if flagJSON {
			printJSON(cfg.Profiles)
			return nil
		}
		if len(cfg.Profiles) == 0 {
			fmt.Println("No profiles saved. Run `chatto login` to create one.")
			return nil
		}
		w := tw()
		fmt.Fprintln(w, bold("PROFILE")+"\t"+bold("INSTANCE")+"\t"+bold("LOGIN")+"\t"+bold("DEFAULT"))
		for name, p := range cfg.Profiles {
			def := ""
			if name == cfg.DefaultProfile {
				def = green("✓")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, p.Instance, p.Login, def)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(meCmd)
	rootCmd.AddCommand(profilesCmd)
}

func loadConfig() (*config.Config, error) {
	return config.Load()
}

// presenceLabel renders a PresenceStatus as a lowercase word, e.g. "online".
func presenceLabel(s apiv1.PresenceStatus) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "PRESENCE_STATUS_"))
}
