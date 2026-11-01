// Package config loads non-secret, local Hewitt preferences.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config contains preferences, not credentials. Loading it creates no directories.
type Config struct {
	// DataDir reserves a location for future session data and worktrees.
	DataDir string `json:"data_dir"`
}

// Load reads path over defaults. An empty path selects the optional default
// config file; a missing explicitly selected file is an error.
func Load(path string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home directory: %w", err)
	}
	cfg := Config{DataDir: filepath.Join(home, ".hewitt")}
	explicit := path != ""
	if !explicit {
		dir, err := os.UserConfigDir()
		if err != nil {
			return Config{}, fmt.Errorf("locate config directory: %w", err)
		}
		path = filepath.Join(dir, "hewitt", "config.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	// Decode an object explicitly so null and non-object documents cannot silently
	// become defaults. Unknown keys catch misspelled preferences early.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if fields == nil {
		return Config{}, fmt.Errorf("config %q must be a JSON object", path)
	}
	for key, value := range fields {
		switch key {
		case "data_dir":
			if string(value) == "null" {
				return Config{}, fmt.Errorf("config %q: data_dir must be a string", path)
			}
			if err := json.Unmarshal(value, &cfg.DataDir); err != nil {
				return Config{}, fmt.Errorf("config %q: decode data_dir: %w", path, err)
			}
		default:
			return Config{}, fmt.Errorf("config %q: unknown field %q", path, key)
		}
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return Config{}, fmt.Errorf("config %q: data_dir must be an absolute path", path)
	}
	return cfg, nil
}
