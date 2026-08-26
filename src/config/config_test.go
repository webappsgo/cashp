package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Mode != "production" {
		t.Errorf("Defaults().Mode = %q, want production", cfg.Mode)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Defaults().Database.Driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Database.Dir != DataDir() {
		t.Errorf("Defaults().Database.Dir = %q, want %q", cfg.Database.Dir, DataDir())
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate(Defaults()) = %v, want nil", err)
	}
}

func TestApplyInitOnlyEnv(t *testing.T) {
	t.Setenv("LISTEN", "127.0.0.1")
	t.Setenv("PORT", "9999")

	cfg := Defaults()
	applyInitOnlyEnv(cfg)

	if cfg.Listen != "127.0.0.1" {
		t.Errorf("Listen = %q, want 127.0.0.1", cfg.Listen)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Port)
	}
}

func TestApplyInitOnlyEnvInvalidPortIgnored(t *testing.T) {
	t.Setenv("PORT", "notanumber")

	cfg := Defaults()
	applyInitOnlyEnv(cfg)

	if cfg.Port != 0 {
		t.Errorf("Port = %d, want unchanged 0 on invalid PORT env", cfg.Port)
	}
}

func TestApplyRuntimeEnv(t *testing.T) {
	t.Setenv("MODE", "debug")
	t.Setenv("DOMAIN", "example.test")
	t.Setenv("DATABASE_DRIVER", "postgres")
	t.Setenv("DATABASE_URL", "postgres://x")

	cfg := Defaults()
	applyRuntimeEnv(cfg)

	if cfg.Mode != "debug" {
		t.Errorf("Mode = %q, want debug", cfg.Mode)
	}
	if cfg.Domain != "example.test" {
		t.Errorf("Domain = %q, want example.test", cfg.Domain)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("Database.Driver = %q, want postgres", cfg.Database.Driver)
	}
	if cfg.Database.URL != "postgres://x" {
		t.Errorf("Database.URL = %q, want postgres://x", cfg.Database.URL)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	cfg := Defaults()
	cfg.Domain = "cashp.test"
	cfg.Port = 12345

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := os.Stat(ConfigFilePath()); err != nil {
		t.Fatalf("expected config file at %q: %v", ConfigFilePath(), err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Domain != "cashp.test" {
		t.Errorf("Load().Domain = %q, want cashp.test", loaded.Domain)
	}
	if loaded.Port != 12345 {
		t.Errorf("Load().Port = %d, want 12345", loaded.Port)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "nonexistent"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mode != "production" {
		t.Errorf("Load() with no file: Mode = %q, want production", cfg.Mode)
	}
}
