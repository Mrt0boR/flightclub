// Package config persists the user's in-app settings: the Discord webhook
// set up from the dashboard's "Setup Discord webhook" screen, and the chosen
// colour theme. It deliberately mirrors internal/history's Load/Save shape —
// same tolerant-of-a-missing-or-corrupt-file behaviour, same atomic write —
// since the two files live side by side and should behave the same way.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fileVersion = 1

// Config is the whole saved document.
type Config struct {
	Version           int    `json:"version"`
	DiscordWebhookURL string `json:"discord_webhook_url,omitempty"`
	Theme             string `json:"theme,omitempty"`
}

// DefaultPath returns the standard per-user location for the config file —
// the same directory history.json lives in.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the user config directory: %w", err)
	}
	return filepath.Join(dir, "flighttrack", "config.json"), nil
}

// Load reads the config file. A missing file is not an error: it returns an
// empty Config, which is what a first run should see. A corrupt file is
// reported but still returns a usable empty Config rather than failing
// startup over one bad write.
func Load(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Version: fileVersion}, nil
	}
	if err != nil {
		return &Config{Version: fileVersion}, err
	}
	var c Config
	if err := json.Unmarshal(buf, &c); err != nil {
		return &Config{Version: fileVersion}, fmt.Errorf("config file is unreadable, starting fresh: %w", err)
	}
	c.Version = fileVersion
	return &c, nil
}

// Save writes the file, creating the directory if needed, via a temporary
// file plus rename so an interrupted write cannot corrupt the previous copy.
func (c *Config) Save(path string) error {
	if path == "" {
		return errors.New("no config path")
	}
	c.Version = fileVersion
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// 0600: this can hold a Discord webhook URL, which is a bearer credential
	// for that one channel.
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
