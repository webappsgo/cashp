// Package config loads, validates, and persists cashp's server
// configuration per AI.md PART 5. server.yml is the single accepted
// config file name (case-sensitive, never server.yaml — auto-migrated on
// startup); YAML comments are single-line, above the setting, under 140
// characters.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the full server.yml structure. Feature-specific sections are
// added as their PARTs are implemented; this is the PART 0-6 foundation
// shape only.
type Config struct {
	Mode     string         `yaml:"mode"`
	Listen   string         `yaml:"listen"`
	Port     int            `yaml:"port"`
	Domain   string         `yaml:"domain"`
	Database DatabaseConfig `yaml:"database"`
}

// DatabaseConfig holds the database connection settings. In single-instance
// mode this is SQLite under DataDir(); in cluster mode it points at the
// shared remote database that becomes the source of truth.
type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	URL    string `yaml:"url,omitempty"`
	Dir    string `yaml:"dir,omitempty"`
}

// Load reads server.yml from ConfigFilePath(), applying init-only
// environment variable overrides on first run and runtime environment
// variable overrides on every load, then validates the result. A missing
// config file is not an error — Defaults() is used and the caller is
// expected to persist it on first run.
func Load() (*Config, error) {
	cfg := Defaults()

	path := ConfigFilePath()
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	applyInitOnlyEnv(cfg)
	applyRuntimeEnv(cfg)

	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Defaults returns a Config populated with the built-in defaults described
// in AI.md PART 5 ("Sane Defaults" design rule).
func Defaults() *Config {
	return &Config{
		Mode:   "production",
		Listen: "0.0.0.0",
		Port:   0,
		Database: DatabaseConfig{
			Driver: "sqlite",
			Dir:    DataDir(),
		},
	}
}

// Save writes cfg to ConfigFilePath(), creating parent directories as
// needed.
func Save(cfg *Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o750); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(ConfigFilePath(), data, 0o640)
}

// applyInitOnlyEnv applies environment variables that are read once, on
// first run, and ignored afterward (AI.md PART 5 "Init-Only Variables").
func applyInitOnlyEnv(cfg *Config) {
	if v := os.Getenv("LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("PORT"); v != "" {
		if p, err := parsePort(v); err == nil {
			cfg.Port = p
		}
	}
}

// applyRuntimeEnv applies environment variables that are checked on every
// load, always taking priority over server.yml (AI.md PART 5 "Runtime
// Variables").
func applyRuntimeEnv(cfg *Config) {
	if v := os.Getenv("MODE"); v != "" {
		cfg.Mode = v
	}
	if v := os.Getenv("DOMAIN"); v != "" {
		cfg.Domain = v
	}
	if v := os.Getenv("DATABASE_DRIVER"); v != "" {
		cfg.Database.Driver = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
}
