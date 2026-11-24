// Package config loads ~/.config/mimir-remarkable/config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

// Config holds every setting; all fields are optional in the file.
type Config struct {
	Dest           string   `toml:"dest"`
	URL            string   `toml:"url"`
	Formats        []string `toml:"formats"`
	Concurrency    int      `toml:"concurrency"`
	PollInterval   Duration `toml:"poll_interval"`
	ResyncInterval Duration `toml:"resync_interval"`
	Notify         bool     `toml:"notify"`
}

// Duration decodes TOML strings such as "5s" or "2m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	d.Duration = v
	return err
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		Dest:           "~/Documents/Remarkable",
		URL:            remarkable.DefaultURL,
		Formats:        []string{string(remarkable.FormatPDF), string(remarkable.FormatRMDoc)},
		Concurrency:    1,
		PollInterval:   Duration{5 * time.Second},
		ResyncInterval: Duration{2 * time.Minute},
		Notify:         true,
	}
}

// DefaultPath is $XDG_CONFIG_HOME/mimir-remarkable/config.toml.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mimir-remarkable", "config.toml"), nil
}

// Load reads path over the defaults. A missing file is an error only if required.
func Load(path string, required bool) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("config %s: unknown keys %v", path, keys)
	}
	return cfg, nil
}

// Validate checks the settings and expands "~" in Dest.
func (c *Config) Validate() error {
	dest, err := ExpandHome(c.Dest)
	if err != nil {
		return err
	}
	if dest == "" {
		return errors.New("dest must not be empty")
	}
	c.Dest = dest
	if _, err := c.ParsedFormats(); err != nil {
		return err
	}
	if c.Concurrency < 1 {
		return errors.New("concurrency must be at least 1")
	}
	if c.PollInterval.Duration <= 0 {
		return errors.New("poll_interval must be positive")
	}
	if c.ResyncInterval.Duration < 0 {
		return errors.New("resync_interval must be positive, or 0 to disable")
	}
	return nil
}

// ParsedFormats returns Formats as remarkable.Format values.
func (c *Config) ParsedFormats() ([]remarkable.Format, error) {
	if len(c.Formats) == 0 {
		return nil, errors.New("formats must not be empty")
	}
	out := make([]remarkable.Format, 0, len(c.Formats))
	for _, s := range c.Formats {
		f, err := remarkable.ParseFormat(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, p[1:]), nil
}
