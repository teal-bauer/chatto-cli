// Package config manages chatto-cli configuration and profiles.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Profile holds connection settings for one chatto server.
type Profile struct {
	Instance string `toml:"instance"`
	Session  string `toml:"session"`
	Login    string `toml:"login,omitempty"`
}

// Config is the top-level config file structure.
type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	Profiles       map[string]Profile `toml:"profiles"`
}

// Path returns the default config file path.
func Path() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "chatto", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "chatto", "config.toml")
}

// Load reads the config file. Returns empty config if file doesn't exist.
func Load() (*Config, error) {
	cfg := &Config{
		Profiles: make(map[string]Profile),
	}
	path := Path()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]Profile)
	}
	return cfg, nil
}

// Save writes the config to disk, creating parent dirs as needed.
func Save(cfg *Config) error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// GetProfile resolves the active profile by name (empty = default).
// instanceOverride, if non-empty, overrides the instance URL.
func GetProfile(profileName, instanceOverride string) (*Profile, string, error) {
	cfg, err := Load()
	if err != nil {
		return nil, "", err
	}

	name := profileName
	if name == "" {
		name = cfg.DefaultProfile
	}
	if name == "" {
		// Fall back to env vars
		p := profileFromEnv()
		if p != nil {
			if instanceOverride != "" {
				p.Instance = instanceOverride
			}
			return p, "env", nil
		}
		return nil, "", fmt.Errorf("no profile configured; run `chatto login` first or set CHATTO_INSTANCE and CHATTO_SESSION")
	}

	p, ok := cfg.Profiles[name]
	if !ok {
		return nil, "", fmt.Errorf("profile %q not found", name)
	}
	if instanceOverride != "" {
		p.Instance = instanceOverride
	}
	return &p, name, nil
}

func profileFromEnv() *Profile {
	instance := os.Getenv("CHATTO_INSTANCE")
	session := os.Getenv("CHATTO_SESSION")
	if instance == "" || session == "" {
		return nil
	}
	return &Profile{Instance: instance, Session: session}
}

// SetProfile saves or updates a profile and optionally makes it the default.
func SetProfile(name string, p Profile, makeDefault bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.Profiles[name] = p
	if makeDefault || cfg.DefaultProfile == "" {
		cfg.DefaultProfile = name
	}
	return Save(cfg)
}

// RemoveProfile deletes a profile from config.
func RemoveProfile(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	delete(cfg.Profiles, name)
	if cfg.DefaultProfile == name {
		cfg.DefaultProfile = ""
	}
	return Save(cfg)
}
