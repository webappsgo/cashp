// Package settings loads, validates and persists cli.yml — the cashp-cli
// configuration file described in AI.md PART 33 "cli.yml Configuration".
package settings

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/webappsgo/cashp/src/client/paths"
	"github.com/webappsgo/cashp/src/config"
)

// Config is the full cli.yml document. Every option in AI.md PART 33 is
// represented so the file round-trips without losing settings.
type Config struct {
	Server   ServerConfig      `yaml:"server"`
	Auth     AuthConfig        `yaml:"auth"`
	Output   OutputConfig      `yaml:"output"`
	TUI      TUIConfig         `yaml:"tui"`
	Logging  LoggingConfig     `yaml:"logging"`
	Cache    CacheConfig       `yaml:"cache"`
	Update   UpdateConfig      `yaml:"update"`
	Display  DisplayConfig     `yaml:"display"`
	Debug    bool              `yaml:"debug"`
	Defaults map[string]string `yaml:"defaults"`
}

// ServerConfig holds the connection target and the discovered cluster list.
type ServerConfig struct {
	Primary    string   `yaml:"primary"`
	Cluster    []string `yaml:"cluster"`
	APIVersion string   `yaml:"api_version"`
	AdminPath  string   `yaml:"admin_path"`
	Timeout    string   `yaml:"timeout"`
	Retry      int      `yaml:"retry"`
	RetryDelay string   `yaml:"retry_delay"`
}

// AuthConfig holds the stored bearer credential or a path to one.
type AuthConfig struct {
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
}

// OutputConfig holds rendering preferences.
type OutputConfig struct {
	Format  string `yaml:"format"`
	Color   string `yaml:"color"`
	Pager   string `yaml:"pager"`
	Quiet   bool   `yaml:"quiet"`
	Verbose bool   `yaml:"verbose"`
}

// TUIConfig holds interactive-mode preferences.
type TUIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Theme   string `yaml:"theme"`
	Mouse   bool   `yaml:"mouse"`
	Unicode bool   `yaml:"unicode"`
}

// LoggingConfig mirrors the server logging block for the CLI log file.
type LoggingConfig struct {
	Level    string `yaml:"level"`
	File     string `yaml:"file"`
	MaxSize  string `yaml:"max_size"`
	MaxFiles int    `yaml:"max_files"`
}

// CacheConfig controls local response caching.
type CacheConfig struct {
	Enabled bool   `yaml:"enabled"`
	TTL     string `yaml:"ttl"`
	MaxSize string `yaml:"max_size"`
}

// UpdateConfig controls CLI self-update behaviour.
type UpdateConfig struct {
	Auto          bool   `yaml:"auto"`
	CheckInterval string `yaml:"check_interval"`
	Channel       string `yaml:"channel"`
}

// DisplayConfig is the only supported way to override display-mode
// auto-detection; no --tui/--cli/--gui flags exist.
type DisplayConfig struct {
	Mode string `yaml:"mode"`
}

// Defaults returns a Config populated with the compiled-in defaults from
// AI.md PART 33.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Primary:    "",
			Cluster:    []string{},
			APIVersion: config.DefaultAPIVersion,
			AdminPath:  "administration",
			Timeout:    "30s",
			Retry:      3,
			RetryDelay: "1s",
		},
		Auth: AuthConfig{},
		Output: OutputConfig{
			Format: "table",
			Color:  "auto",
			Pager:  "auto",
		},
		TUI: TUIConfig{
			Enabled: true,
			Theme:   "dark",
			Mouse:   true,
			Unicode: true,
		},
		Logging: LoggingConfig{
			Level:    "warn",
			MaxSize:  "10MB",
			MaxFiles: 5,
		},
		Cache: CacheConfig{
			Enabled: true,
			TTL:     "5m",
			MaxSize: "100MB",
		},
		Update: UpdateConfig{
			Auto:          false,
			CheckInterval: "per_invocation",
			Channel:       "stable",
		},
		Display: DisplayConfig{Mode: "auto"},
		Defaults: map[string]string{
			"lang":   "auto",
			"output": "table",
			"limit":  "50",
		},
	}
}

// Load reads path into a Config layered over the compiled defaults. A
// missing file is not an error — the defaults are returned so a first run
// works with zero configuration. The permission gate runs first: a
// group/world-readable cli.yml is refused rather than silently trusted.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if err := paths.CheckFilePerms(path); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	normalize(cfg)
	return cfg, nil
}

// Save writes cfg to path with owner-only permissions.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return paths.WriteSecureFile(path, data)
}

// normalize backfills any field the user blanked out in cli.yml so the rest
// of the CLI never has to handle an empty required value.
func normalize(cfg *Config) {
	def := Defaults()
	if strings.TrimSpace(cfg.Server.APIVersion) == "" {
		cfg.Server.APIVersion = def.Server.APIVersion
	}
	if strings.TrimSpace(cfg.Server.AdminPath) == "" {
		cfg.Server.AdminPath = def.Server.AdminPath
	}
	if strings.TrimSpace(cfg.Server.Timeout) == "" {
		cfg.Server.Timeout = def.Server.Timeout
	}
	if cfg.Server.Retry <= 0 {
		cfg.Server.Retry = def.Server.Retry
	}
	if strings.TrimSpace(cfg.Server.RetryDelay) == "" {
		cfg.Server.RetryDelay = def.Server.RetryDelay
	}
	if strings.TrimSpace(cfg.Output.Format) == "" {
		cfg.Output.Format = def.Output.Format
	}
	if strings.TrimSpace(cfg.Output.Color) == "" {
		cfg.Output.Color = def.Output.Color
	}
	if strings.TrimSpace(cfg.Display.Mode) == "" {
		cfg.Display.Mode = def.Display.Mode
	}
	if cfg.Defaults == nil {
		cfg.Defaults = map[string]string{}
	}
	if cfg.Server.Cluster == nil {
		cfg.Server.Cluster = []string{}
	}
}

// SaveIfEmptyOrInvalid implements the AI.md PART 33 "Flag-to-Config Save
// Rules" table. It returns the value to use for this invocation and whether
// the caller should persist it to cli.yml.
func SaveIfEmptyOrInvalid(current, flagValue string, validate func(string) bool) (use string, persist bool) {
	if flagValue == "" {
		return current, false
	}
	if !validate(flagValue) {
		return current, false
	}
	if current == "" || !validate(current) {
		return flagValue, true
	}
	return flagValue, false
}
