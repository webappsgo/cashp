// Package settings loads, validates and persists agent.yml — the cashp
// agent configuration described in AI.md PART 33 "agent.yml Configuration".
package settings

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/config"
)

// EnvPrefix is the environment-variable namespace for agent overrides.
var EnvPrefix = strings.ToUpper(config.InternalName) + "_AGENT_"

// Config is the full agent.yml document.
type Config struct {
	Lang       string           `yaml:"lang"`
	Server     ServerConfig     `yaml:"server"`
	Auth       AuthConfig       `yaml:"auth"`
	Identity   IdentityConfig   `yaml:"identity"`
	Collection CollectionConfig `yaml:"collection"`
	Logging    LoggingConfig    `yaml:"logging"`
	Health     HealthConfig     `yaml:"health"`
	Debug      bool             `yaml:"debug"`
	Mode       string           `yaml:"mode"`
}

// ServerConfig holds the connection target and the discovered cluster list.
type ServerConfig struct {
	Primary        string   `yaml:"primary"`
	Cluster        []string `yaml:"cluster"`
	APIVersion     string   `yaml:"api_version"`
	AdminPath      string   `yaml:"admin_path"`
	Timeout        string   `yaml:"timeout"`
	Retry          int      `yaml:"retry"`
	RetryDelay     string   `yaml:"retry_delay"`
	ReconnectDelay string   `yaml:"reconnect_delay"`
}

// AuthConfig holds the agent token or a path to a file containing it.
type AuthConfig struct {
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	// OrgSlug names the owning organization. It is required only for an
	// org-scoped token, whose routes are /orgs/{slug}/agents.
	OrgSlug string `yaml:"org_slug"`
}

// IdentityConfig describes how this node presents itself to the panel.
type IdentityConfig struct {
	Hostname    string            `yaml:"hostname"`
	DisplayName string            `yaml:"display_name"`
	Tags        []string          `yaml:"tags"`
	Labels      map[string]string `yaml:"labels"`
}

// CollectionConfig controls the metric collection loop.
type CollectionConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Interval   string `yaml:"interval"`
	BatchSize  int    `yaml:"batch_size"`
	BufferSize int    `yaml:"buffer_size"`
}

// LoggingConfig mirrors the server logging block.
type LoggingConfig struct {
	Level    string `yaml:"level"`
	File     string `yaml:"file"`
	MaxSize  string `yaml:"max_size"`
	MaxFiles int    `yaml:"max_files"`
}

// HealthConfig controls the heartbeat loop.
type HealthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Interval string `yaml:"interval"`
}

// Defaults returns the compiled-in configuration from AI.md PART 33.
func Defaults() *Config {
	return &Config{
		Lang: "auto",
		Server: ServerConfig{
			Primary:        "",
			Cluster:        []string{},
			APIVersion:     config.DefaultAPIVersion,
			AdminPath:      "administration",
			Timeout:        "30s",
			Retry:          3,
			RetryDelay:     "5s",
			ReconnectDelay: "10s",
		},
		Identity: IdentityConfig{
			Tags:   []string{},
			Labels: map[string]string{},
		},
		Collection: CollectionConfig{
			Enabled:    true,
			Interval:   "60s",
			BatchSize:  100,
			BufferSize: 1000,
		},
		Logging: LoggingConfig{
			Level:    "info",
			MaxSize:  "10MB",
			MaxFiles: 5,
		},
		Health: HealthConfig{
			Enabled:  true,
			Interval: "30s",
		},
	}
}

// Load reads path over the compiled defaults. A missing file is not an
// error: a freshly installed agent is expected to be configured by its
// first `--server ... --token ...` invocation. The permission gate runs
// first because agent.yml may carry the enrollment token.
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

	Normalize(cfg)
	return cfg, nil
}

// Save writes cfg to path with owner-only permissions.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode agent config: %w", err)
	}
	return paths.WriteSecureFile(path, data)
}

// Normalize backfills any required field the operator blanked out so the
// rest of the agent never has to handle an empty required value.
func Normalize(cfg *Config) {
	def := Defaults()
	if strings.TrimSpace(cfg.Lang) == "" {
		cfg.Lang = def.Lang
	}
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
	if strings.TrimSpace(cfg.Server.ReconnectDelay) == "" {
		cfg.Server.ReconnectDelay = def.Server.ReconnectDelay
	}
	if strings.TrimSpace(cfg.Collection.Interval) == "" {
		cfg.Collection.Interval = def.Collection.Interval
	}
	if cfg.Collection.BatchSize <= 0 {
		cfg.Collection.BatchSize = def.Collection.BatchSize
	}
	if cfg.Collection.BufferSize <= 0 {
		cfg.Collection.BufferSize = def.Collection.BufferSize
	}
	if strings.TrimSpace(cfg.Logging.Level) == "" {
		cfg.Logging.Level = def.Logging.Level
	}
	if cfg.Logging.MaxFiles <= 0 {
		cfg.Logging.MaxFiles = def.Logging.MaxFiles
	}
	if strings.TrimSpace(cfg.Health.Interval) == "" {
		cfg.Health.Interval = def.Health.Interval
	}
	if cfg.Server.Cluster == nil {
		cfg.Server.Cluster = []string{}
	}
	if cfg.Identity.Tags == nil {
		cfg.Identity.Tags = []string{}
	}
	if cfg.Identity.Labels == nil {
		cfg.Identity.Labels = map[string]string{}
	}
}

// ApplyEnv layers environment overrides on top of the loaded file, matching
// the AI.md PART 33 precedence table: flag > environment > file > default.
// Only values that are actually present in the environment are applied.
func ApplyEnv(cfg *Config, lookup func(string) (string, bool)) {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	setString(lookup, EnvPrefix+"SERVER_PRIMARY", func(v string) { cfg.Server.Primary = v })
	setString(lookup, EnvPrefix+"SERVER", func(v string) { cfg.Server.Primary = v })
	setString(lookup, EnvPrefix+"TOKEN", func(v string) { cfg.Auth.Token = v })
	setString(lookup, EnvPrefix+"TOKEN_FILE", func(v string) { cfg.Auth.TokenFile = v })
	setString(lookup, EnvPrefix+"ORG_SLUG", func(v string) { cfg.Auth.OrgSlug = v })
	setString(lookup, EnvPrefix+"HOSTNAME", func(v string) { cfg.Identity.Hostname = v })
	setString(lookup, EnvPrefix+"DISPLAY_NAME", func(v string) { cfg.Identity.DisplayName = v })
	setString(lookup, EnvPrefix+"API_VERSION", func(v string) { cfg.Server.APIVersion = v })
	setString(lookup, EnvPrefix+"ADMIN_PATH", func(v string) { cfg.Server.AdminPath = v })
	setString(lookup, EnvPrefix+"LOG_LEVEL", func(v string) { cfg.Logging.Level = v })
	setString(lookup, EnvPrefix+"MODE", func(v string) { cfg.Mode = v })
	setString(lookup, EnvPrefix+"LANG", func(v string) { cfg.Lang = v })

	if value, ok := lookup(EnvPrefix + "TAGS"); ok {
		cfg.Identity.Tags = SplitList(value)
	}
	if value, ok := lookup(EnvPrefix + "COLLECTION_INTERVAL"); ok {
		cfg.Collection.Interval = NormalizeDuration(value)
	}
	if value, ok := lookup(EnvPrefix + "HEALTH_INTERVAL"); ok {
		cfg.Health.Interval = NormalizeDuration(value)
	}
	if value, ok := lookup(EnvPrefix + "COLLECTION_ENABLED"); ok {
		if parsed, err := config.ParseBool(value, cfg.Collection.Enabled); err == nil {
			cfg.Collection.Enabled = parsed
		}
	}
	if value, ok := lookup(EnvPrefix + "HEALTH_ENABLED"); ok {
		if parsed, err := config.ParseBool(value, cfg.Health.Enabled); err == nil {
			cfg.Health.Enabled = parsed
		}
	}
	if value, ok := lookup(strings.ToUpper(config.InternalName) + "_DEBUG"); ok {
		if parsed, err := config.ParseBool(value, cfg.Debug); err == nil {
			cfg.Debug = parsed
		}
	}
}

// SplitList parses a comma-separated list, dropping empty entries.
func SplitList(value string) []string {
	items := []string{}
	for _, part := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// NormalizeDuration accepts either a Go duration ("30s") or a bare number
// of seconds, which the documented environment examples use.
func NormalizeDuration(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return trimmed
	}
	if _, err := strconv.Atoi(trimmed); err == nil {
		return trimmed + "s"
	}
	return trimmed
}

// setString applies an environment value when the variable is present and
// non-empty.
func setString(lookup func(string) (string, bool), name string, apply func(string)) {
	value, ok := lookup(name)
	if !ok {
		return
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	apply(trimmed)
}
