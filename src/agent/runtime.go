package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/agent/agentlog"
	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/agent/settings"
	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/common/display"
	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/mode"
)

// ErrNoToken is returned when nothing on the command line, in the
// environment, or on disk supplies an agent token.
var ErrNoToken = errors.New("no agent token; pass --token or run the connect command from the panel")

// ErrNoServer is returned when no panel URL is configured.
var ErrNoServer = errors.New("no server URL; pass --server or set it in agent.yml")

// Runtime is everything a command needs: the resolved configuration, the
// directory overrides it was resolved against, and an authenticated
// transport client.
type Runtime struct {
	Options   *Options
	Config    *settings.Config
	Overrides paths.Overrides
	Client    *transport.Client
	Color     *bool
	Mode      mode.Mode
	Debug     bool
	Logger    *slog.Logger
	Closer    interface{ Close() error }
}

// Overrides builds the directory overrides from the parsed flags.
func (o *Options) Directories() paths.Overrides {
	return paths.Overrides{
		Config: strings.TrimSpace(o.ConfigDir),
		Data:   strings.TrimSpace(o.DataDir),
		Log:    strings.TrimSpace(o.LogDir),
	}
}

// LoadConfig applies the documented precedence: compiled defaults, then
// agent.yml, then the environment, then the command line.
func LoadConfig(opts *Options) (*settings.Config, paths.Overrides, error) {
	overrides := opts.Directories()

	cfg, err := settings.Load(paths.ConfigFile(overrides))
	if err != nil {
		return nil, overrides, err
	}
	settings.ApplyEnv(cfg, os.LookupEnv)

	if value := strings.TrimSpace(opts.Server); value != "" {
		cfg.Server.Primary = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(opts.Token); value != "" {
		cfg.Auth.Token = value
	}
	if value := strings.TrimSpace(opts.Org); value != "" {
		cfg.Auth.OrgSlug = value
	}
	if value := strings.TrimSpace(opts.Mode); value != "" {
		cfg.Mode = value
	}
	if value := strings.TrimSpace(opts.Lang); value != "" {
		cfg.Lang = value
	}
	if opts.Debug {
		cfg.Debug = true
	}

	settings.Normalize(cfg)
	return cfg, overrides, nil
}

// NewRuntime resolves the configuration, the token and the transport for
// one command. withLog controls whether agent.log is opened, which only the
// long-running foreground command wants.
func NewRuntime(opts *Options, withLog bool) (*Runtime, error) {
	cfg, overrides, err := LoadConfig(opts)
	if err != nil {
		return nil, err
	}

	appMode := mode.Resolve(cfg.Mode)
	debug := mode.ResolveDebug(&cfg.Debug, appMode)

	logDir := ""
	if withLog {
		if err := paths.EnsureDirs(overrides); err != nil {
			return nil, err
		}
		logDir = paths.LogDir(overrides)
	}

	logger, closer, err := agentlog.New(agentlog.Options{
		Dir:     logDir,
		Level:   cfg.Logging.Level,
		Debug:   debug,
		Console: true,
	})
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{
		Options:   opts,
		Config:    cfg,
		Overrides: overrides,
		Color:     display.ParseColorFlag(opts.Color),
		Mode:      appMode,
		Debug:     debug,
		Logger:    logger,
		Closer:    closer,
	}

	if err := runtime.connect(); err != nil {
		_ = closer.Close()
		return nil, err
	}
	return runtime, nil
}

// connect builds the transport client from the resolved configuration.
func (r *Runtime) connect() error {
	server := strings.TrimRight(strings.TrimSpace(r.Config.Server.Primary), "/")
	if server == "" {
		return ErrNoServer
	}
	if err := transport.ValidateServerURL(server); err != nil {
		return err
	}

	token, err := ResolveToken(r.Config, r.Overrides, r.Options.Token)
	if err != nil {
		return err
	}

	client, err := transport.New(transport.Options{
		Primary:    server,
		Cluster:    r.Config.Server.Cluster,
		Token:      token,
		Version:    Version,
		APIVersion: r.Config.Server.APIVersion,
		AdminPath:  r.Config.Server.AdminPath,
		OrgSlug:    r.Config.Auth.OrgSlug,
		Timeout:    ParseDuration(r.Config.Server.Timeout, 30*time.Second),
		Retry:      r.Config.Server.Retry,
		RetryDelay: ParseDuration(r.Config.Server.RetryDelay, 5*time.Second),
	})
	if err != nil {
		return err
	}

	r.Client = client
	return nil
}

// Close releases the log file, if one was opened.
func (r *Runtime) Close() {
	if r.Closer != nil {
		_ = r.Closer.Close()
	}
}

// ResolveToken applies the token precedence: command line, then the
// configuration (which already carries any environment override), then the
// token file. The token file's permissions are checked before it is read.
func ResolveToken(cfg *settings.Config, overrides paths.Overrides, flagToken string) (string, error) {
	if value := strings.TrimSpace(flagToken); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(cfg.Auth.Token); value != "" {
		return value, nil
	}

	path := strings.TrimSpace(cfg.Auth.TokenFile)
	if path == "" {
		path = paths.TokenFile(overrides)
	}
	if err := paths.CheckFilePerms(path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// SaveEnrollment persists the enrollment: the token goes into its own
// 0600 file and agent.yml records only the path to it, so the panel
// credential never sits in a file an operator is likely to share.
func SaveEnrollment(cfg *settings.Config, overrides paths.Overrides, token string) error {
	if err := paths.EnsureDirs(overrides); err != nil {
		return err
	}

	tokenPath := strings.TrimSpace(cfg.Auth.TokenFile)
	if tokenPath == "" {
		tokenPath = paths.TokenFile(overrides)
	}
	if err := paths.WriteSecureFile(tokenPath, []byte(token+"\n")); err != nil {
		return err
	}

	stored := *cfg
	stored.Auth.Token = ""
	stored.Auth.TokenFile = tokenPath
	return settings.Save(paths.ConfigFile(overrides), &stored)
}

// ParseDuration reads a duration string, falling back when it is empty or
// malformed so a hand-edited agent.yml cannot disable a timeout.
func ParseDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// AppName is the display name used in the banner and status output.
func AppName() string {
	return config.InternalName + "-agent"
}
