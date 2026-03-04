package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/teal-bauer/chatto-cli/config"
)

var meCmd = &cobra.Command{
	Use:   "me",
	Short: "Show the current authenticated user",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := clientFromFlags()
		if err != nil {
			return err
		}
		me, err := c.Me()
		if err != nil {
			return err
		}
		if me == nil {
			return fmt.Errorf("not authenticated")
		}
		if flagJSON {
			printJSON(me)
			return nil
		}
		w := tw()
		fmt.Fprintf(w, "Login:\t%s\n", me.Login)
		fmt.Fprintf(w, "Display name:\t%s\n", me.DisplayName)
		fmt.Fprintf(w, "ID:\t%s\n", dim(me.ID))
		fmt.Fprintf(w, "Presence:\t%s\n", me.PresenceStatus)
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
