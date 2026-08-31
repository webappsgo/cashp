package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.AdminPath != "administration" {
		t.Errorf("Server.AdminPath = %q, want administration", cfg.Server.AdminPath)
	}
	if cfg.Server.APIVersion != "v1" {
		t.Errorf("Server.APIVersion = %q, want v1", cfg.Server.APIVersion)
	}
	if cfg.Server.Database.Driver != "sqlite" {
		t.Errorf("Server.Database.Driver = %q, want sqlite", cfg.Server.Database.Driver)
	}
	if cfg.Server.Database.Dir != DataDir() {
		t.Errorf("Server.Database.Dir = %q, want %q", cfg.Server.Database.Dir, DataDir())
	}
	if cfg.Server.Cache.Type != "memory" {
		t.Errorf("Server.Cache.Type = %q, want memory", cfg.Server.Cache.Type)
	}
	if !cfg.Server.Features.MultiUser.Value || !cfg.Server.Features.Organizations.Value || !cfg.Server.Features.CustomDomains.Enabled.Value {
		t.Error("multi_user, organizations, and custom_domains must all default to true")
	}
	if got := cfg.Server.Session.Admin.MaxAge.Value; got != 30*24*time.Hour {
		t.Errorf("session.admin.max_age = %v, want 720h", got)
	}
	if got := cfg.Server.Session.User.MaxAge.Value; got != 7*24*time.Hour {
		t.Errorf("session.user.max_age = %v, want 168h", got)
	}
	if got := cfg.Server.Session.Admin.IdleTimeout.Value; got != 24*time.Hour {
		t.Errorf("session.admin.idle_timeout = %v, want 24h", got)
	}
	if !cfg.Server.Privacy.Consent.DefaultEnabled.Value {
		t.Error("cookie consent must default to opt-out (default_enabled true)")
	}
	if cfg.Server.RateLimit.Read.Requests != 120 || cfg.Server.RateLimit.Write.Requests != 10 {
		t.Errorf("rate limits = read %d write %d, want 120 and 10",
			cfg.Server.RateLimit.Read.Requests, cfg.Server.RateLimit.Write.Requests)
	}
}

func TestDefaultsValidateWithoutHardFailure(t *testing.T) {
	cfg := Defaults()

	for _, w := range Validate(cfg) {
		// The default tree is expected to need no repairs at all.
		t.Errorf("Validate(Defaults()) warned: %s", w)
	}

	if cfg.Server.Port.HTTP < DefaultPortRangeMin || cfg.Server.Port.HTTP > DefaultPortRangeMax {
		t.Errorf("Port.HTTP = %d, want a port in %d-%d", cfg.Server.Port.HTTP, DefaultPortRangeMin, DefaultPortRangeMax)
	}
	if cfg.Server.Security.EncryptionKey == "" {
		t.Error("Validate must generate security.encryption_key on first run")
	}
}

func TestUnmarshalKeepsDefaultsForOmittedKeys(t *testing.T) {
	cfg := Defaults()

	data := []byte("server:\n  mode: development\n  port: 8080\n")
	if err := yaml.Unmarshal(data, cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.Server.Mode != "development" {
		t.Errorf("Server.Mode = %q, want development", cfg.Server.Mode)
	}
	if cfg.Server.Port.HTTP != 8080 {
		t.Errorf("Server.Port.HTTP = %d, want 8080", cfg.Server.Port.HTTP)
	}
	if cfg.Server.AdminPath != "administration" {
		t.Errorf("omitted admin_path = %q, want the default administration", cfg.Server.AdminPath)
	}
}

func TestUnmarshalNestedSections(t *testing.T) {
	cfg := Defaults()

	data := []byte(`
server:
  limits:
    max_body_size: 25MB
    read_timeout: 45s
  session:
    admin:
      max_age: 14d
  contact:
    security:
      email: soc@example.test
      webhooks:
        slack: https://hooks.example.test/soc
  features:
    tor: true
`)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.Server.Limits.MaxBodySize.Value != 25<<20 {
		t.Errorf("max_body_size = %d, want %d", cfg.Server.Limits.MaxBodySize.Value, 25<<20)
	}
	if cfg.Server.Limits.ReadTimeout.Value != 45*time.Second {
		t.Errorf("read_timeout = %v, want 45s", cfg.Server.Limits.ReadTimeout.Value)
	}
	if cfg.Server.Session.Admin.MaxAge.Value != 14*24*time.Hour {
		t.Errorf("session.admin.max_age = %v, want 336h", cfg.Server.Session.Admin.MaxAge.Value)
	}
	if cfg.Server.Contact.Security.Webhooks["slack"] != "https://hooks.example.test/soc" {
		t.Errorf("security slack webhook = %q", cfg.Server.Contact.Security.Webhooks["slack"])
	}
	if !cfg.Server.Features.Tor.Value {
		t.Error("features.tor = false, want true")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Port = NewPortSpec(64500)
	cfg.Server.FQDN = "cashp.test"

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	reloaded := Defaults()
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if reloaded.Server.Port.HTTP != 64500 {
		t.Errorf("round-tripped port = %d, want 64500", reloaded.Server.Port.HTTP)
	}
	if reloaded.Server.FQDN != "cashp.test" {
		t.Errorf("round-tripped fqdn = %q, want cashp.test", reloaded.Server.FQDN)
	}
	if reloaded.Server.Session.User.MaxAge.Value != 7*24*time.Hour {
		t.Errorf("round-tripped session.user.max_age = %v, want 168h", reloaded.Server.Session.User.MaxAge.Value)
	}
}

func TestApplyInitOnlyEnv(t *testing.T) {
	t.Setenv("LISTEN", "127.0.0.1")
	t.Setenv("PORT", "9999")
	t.Setenv("APPLICATION_NAME", "Panel")

	cfg := Defaults()
	applyInitOnlyEnv(cfg)

	if cfg.Server.Address != "127.0.0.1" {
		t.Errorf("Server.Address = %q, want 127.0.0.1", cfg.Server.Address)
	}
	if cfg.Server.Port.HTTP != 9999 {
		t.Errorf("Server.Port.HTTP = %d, want 9999", cfg.Server.Port.HTTP)
	}
	if cfg.Server.Branding.Title != "Panel" {
		t.Errorf("Server.Branding.Title = %q, want Panel", cfg.Server.Branding.Title)
	}
}

func TestApplyInitOnlyEnvInvalidPortIgnored(t *testing.T) {
	t.Setenv("PORT", "notanumber")

	cfg := Defaults()
	applyInitOnlyEnv(cfg)

	if cfg.Server.Port.HTTP != 0 {
		t.Errorf("Server.Port.HTTP = %d, want the unset 0 on an invalid PORT", cfg.Server.Port.HTTP)
	}
}

func TestApplyRuntimeEnv(t *testing.T) {
	t.Setenv("MODE", "debug")
	t.Setenv("DOMAIN", "example.test")
	t.Setenv("DATABASE_DRIVER", "postgres")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("SMTP_HOST", "mail.example.test")
	t.Setenv("SMTP_PORT", "587")

	cfg := Defaults()
	applyRuntimeEnv(cfg)

	if cfg.Server.Mode != "debug" {
		t.Errorf("Server.Mode = %q, want debug", cfg.Server.Mode)
	}
	if cfg.Server.FQDN != "example.test" {
		t.Errorf("Server.FQDN = %q, want example.test", cfg.Server.FQDN)
	}
	if cfg.Server.Database.Driver != "postgres" {
		t.Errorf("Server.Database.Driver = %q, want postgres", cfg.Server.Database.Driver)
	}
	if cfg.Server.Database.URL != "postgres://x" {
		t.Errorf("Server.Database.URL = %q, want postgres://x", cfg.Server.Database.URL)
	}
	if cfg.Server.Notifications.Email.SMTP.Host != "mail.example.test" {
		t.Errorf("SMTP.Host = %q, want mail.example.test", cfg.Server.Notifications.Email.SMTP.Host)
	}
	if cfg.Server.Notifications.Email.SMTP.Port != 587 {
		t.Errorf("SMTP.Port = %d, want 587", cfg.Server.Notifications.Email.SMTP.Port)
	}
}

// withTempHome points the config directory at a throwaway home. Running as
// root selects the system-wide /etc path, which a test must never touch, so
// those runs skip instead.
func withTempHome(t *testing.T) {
	t.Helper()

	if isRoot() {
		t.Skip("config paths are system-wide when running as root")
	}

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestSaveAndLoad(t *testing.T) {
	withTempHome(t)

	cfg := Defaults()
	cfg.Server.FQDN = "cashp.test"
	cfg.Server.Port = NewPortSpec(12345)

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
	if loaded.Server.FQDN != "cashp.test" {
		t.Errorf("Load().Server.FQDN = %q, want cashp.test", loaded.Server.FQDN)
	}
	if loaded.Server.Port.HTTP != 12345 {
		t.Errorf("Load().Server.Port.HTTP = %d, want 12345", loaded.Server.Port.HTTP)
	}
}

func TestLoadFirstRunPersistsRandomPort(t *testing.T) {
	withTempHome(t)

	cfg, warnings, err := LoadWithWarnings()
	if err != nil {
		t.Fatalf("LoadWithWarnings() error = %v", err)
	}
	for _, w := range warnings {
		t.Errorf("unexpected warning on a clean first run: %s", w)
	}

	if cfg.Server.Port.HTTP < DefaultPortRangeMin || cfg.Server.Port.HTTP > DefaultPortRangeMax {
		t.Fatalf("first-run port = %d, want %d-%d", cfg.Server.Port.HTTP, DefaultPortRangeMin, DefaultPortRangeMax)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Server.Port.HTTP != cfg.Server.Port.HTTP {
		t.Errorf("port not persisted: got %d, want %d", reloaded.Server.Port.HTTP, cfg.Server.Port.HTTP)
	}
	if reloaded.Server.Security.EncryptionKey != cfg.Server.Security.EncryptionKey {
		t.Error("encryption key must be persisted on first run, not regenerated each start")
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	withTempHome(t)

	if err := os.MkdirAll(ConfigDir(), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := filepath.Join(ConfigDir(), "server.yaml")
	if err := os.WriteFile(legacy, []byte("server:\n  mode: development\n"), 0o640); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy server.yaml still present: %v", err)
	}
	if _, err := os.Stat(ConfigFilePath()); err != nil {
		t.Fatalf("expected migrated server.yml: %v", err)
	}
	if cfg.Server.Mode != "development" {
		t.Errorf("migrated Server.Mode = %q, want development", cfg.Server.Mode)
	}
}

func TestMigrateLegacyConfigKeepsExistingCurrent(t *testing.T) {
	withTempHome(t)

	if err := os.MkdirAll(ConfigDir(), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(LegacyConfigFilePath(), []byte("server:\n  mode: debug\n"), 0o640); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(ConfigFilePath(), []byte("server:\n  mode: development\n"), 0o640); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := MigrateLegacyConfig(); err != nil {
		t.Fatalf("MigrateLegacyConfig() error = %v", err)
	}

	data, err := os.ReadFile(ConfigFilePath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "server:\n  mode: development\n" {
		t.Errorf("server.yml was overwritten by the legacy file: %q", data)
	}
	if _, err := os.Stat(LegacyConfigFilePath()); err != nil {
		t.Errorf("legacy file should be left in place: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	withTempHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Load() with no file: Server.Mode = %q, want production", cfg.Server.Mode)
	}
}
