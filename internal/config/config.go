// Package config loads shelf's optional TOML configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the contents of the shelf configuration file.
type Config struct {
	// Library is the default directory that commands search for EPUB files.
	// An empty value means no library was configured.
	Library string `toml:"library"`

	// Viewer is the command used to open an EPUB from the TUI. The selected
	// file path is appended as the final argument.
	Viewer string `toml:"viewer"`

	// Sort is the default Books pane ordering, e.g. "title" or "date:desc".
	Sort string `toml:"sort"`

	// SearchURL is opened in the default browser for the selected book. It may
	// contain the same metadata placeholders as rename_format.
	SearchURL string `toml:"search_url"`

	// Theme selects the TUI colour palette. The default is "gruvbox".
	Theme string `toml:"theme"`

	// GoogleBooksAPIKey is an optional Google Books API key. Unauthenticated
	// requests share a small quota and often return 429.
	GoogleBooksAPIKey string `toml:"google_books_api_key"`

	// RenameFormat is the default file-name pattern for the rename command.
	RenameFormat string `toml:"rename_format"`

	// Slugify makes the rename command use slugified values by default.
	Slugify bool `toml:"slugify"`

	// Sluggify is accepted as an alias for Slugify.
	Sluggify bool `toml:"sluggify"`
}

// SlugifyRename reports whether rename should slugify by default.
func (c Config) SlugifyRename() bool {
	return c.Slugify || c.Sluggify
}

// Path returns the XDG configuration file path:
// $XDG_CONFIG_HOME/shelf/config.toml, or ~/.config/shelf/config.toml when
// XDG_CONFIG_HOME is unset.
func Path() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "shelf", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the configuration directory: %w", err)
	}
	return filepath.Join(home, ".config", "shelf", "config.toml"), nil
}

// Load reads the configuration from the XDG location. A missing file is not
// an error and yields the zero Config.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path)
}

// LoadFile reads the configuration at path. A missing file is not an error.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Library = expandHome(strings.TrimSpace(cfg.Library))
	cfg.Viewer = strings.TrimSpace(cfg.Viewer)
	cfg.Sort = strings.TrimSpace(cfg.Sort)
	cfg.SearchURL = strings.TrimSpace(cfg.SearchURL)
	cfg.Theme = strings.TrimSpace(cfg.Theme)
	cfg.GoogleBooksAPIKey = strings.TrimSpace(cfg.GoogleBooksAPIKey)
	cfg.RenameFormat = strings.TrimSpace(cfg.RenameFormat)
	return cfg, nil
}

// LibraryDir returns the configured library, or fallback when none is set.
func (c Config) LibraryDir(fallback string) string {
	if c.Library != "" {
		return c.Library
	}
	return fallback
}

// expandHome expands a leading ~ or ~/ to the user's home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
