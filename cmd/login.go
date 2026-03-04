package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/teal-bauer/chatto-cli/api"
	"github.com/teal-bauer/chatto-cli/config"
)

var loginCmd = &cobra.Command{
	Use:   "login [profile]",
	Short: "Log in to a Chatto instance and save the session",
	Long: `Authenticates with a Chatto instance using email/password and stores the session
token in the named profile (default: "default").

Examples:
  chatto login
  chatto login myserver --instance https://chat.example.com
  chatto login work --instance https://work.chatto.run --email me@example.com`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLogin,
}

var (
	loginEmail    string
	loginPassword string
)

func init() {
	loginCmd.Flags().StringVar(&loginEmail, "email", "", "email address")
	loginCmd.Flags().StringVar(&loginPassword, "password", "", "password (will prompt if omitted)")
	rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, args []string) error {
	profileName := "default"
	if len(args) > 0 {
		profileName = args[0]
	}

	instance := flagInstance
	if instance == "" {
		// try to load existing profile's instance
		cfg, _ := config.Load()
		if cfg != nil {
			if p, ok := cfg.Profiles[profileName]; ok {
				instance = p.Instance
			}
		}
	}
	if instance == "" {
		fmt.Print("Instance URL (e.g. https://chatto.run): ")
		fmt.Scanln(&instance)
	}
	if instance == "" {
		return fmt.Errorf("instance URL is required")
	}

	email := loginEmail
	if email == "" {
		fmt.Print("Username or Email: ")
		fmt.Scanln(&email)
	}

	password := loginPassword
	if password == "" {
		password = readPassword("Password: ")
	}

	fmt.Printf("Logging in to %s as %s...\n", instance, email)
	session, err := api.Login(instance, email, password)
	if err != nil {
		return err
	}

	// Verify by fetching current user
	client := api.New(instance, session)
	me, err := client.Me()
	if err != nil {
		return fmt.Errorf("login succeeded but could not fetch user info: %w", err)
	}

	displayName := me.Login
	if me.DisplayName != "" {
		displayName = me.DisplayName
	}

	prof := config.Profile{
		Instance: instance,
		Session:  session,
		Login:    me.Login,
	}

	cfg, _ := config.Load()
	makeDefault := cfg == nil || cfg.DefaultProfile == "" || cfg.DefaultProfile == profileName
	if err := config.SetProfile(profileName, prof, makeDefault); err != nil {
		return fmt.Errorf("saving profile: %w", err)
	}

	fmt.Printf("Logged in as %s. Profile %q saved.\n", bold(displayName), profileName)
	return nil
}

var logoutCmd = &cobra.Command{
	Use:   "logout [profile]",
	Short: "Remove a stored session",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName := "default"
		if len(args) > 0 {
			profileName = args[0]
		}
		if err := config.RemoveProfile(profileName); err != nil {
			return err
		}
		fmt.Printf("Profile %q removed.\n", profileName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
