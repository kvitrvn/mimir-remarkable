package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.toml")
	cfg, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval.Duration != 5*time.Second || !cfg.Notify {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if _, err := Load(p, true); err == nil {
		t.Error("required missing file: want error")
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	cfg, err := Load(write(t, `
dest = "~/rm"
formats = ["pdf"]
resync_interval = "0s"
notify = false
`), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if cfg.Dest != filepath.Join(home, "rm") {
		t.Errorf("Dest = %q", cfg.Dest)
	}
	if !slices.Equal(cfg.Formats, []string{"pdf"}) || cfg.ResyncInterval.Duration != 0 || cfg.Notify {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.PollInterval.Duration != 5*time.Second || cfg.URL == "" {
		t.Errorf("unset keys lost their defaults: %+v", cfg)
	}
}

func TestInvalid(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":  `destination = "x"`,
		"bad duration": `poll_interval = "fast"`,
	} {
		if _, err := Load(write(t, content), true); err == nil {
			t.Errorf("%s: want load error", name)
		}
	}
	for name, content := range map[string]string{
		"bad format":    `formats = ["docx"]`,
		"zero poll":     `poll_interval = "0s"`,
		"no formats":    `formats = []`,
		"concurrency 0": `concurrency = 0`,
	} {
		cfg, err := Load(write(t, content), true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: want validation error", name)
		}
	}
}
