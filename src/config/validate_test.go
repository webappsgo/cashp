package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// validated decodes a server.yml fragment over the defaults and validates
// it, returning the repaired config and the warnings it produced.
func validated(t *testing.T, doc string) (*Config, []Warning) {
	t.Helper()

	cfg := Defaults()
	if err := yaml.Unmarshal([]byte(doc), cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	return cfg, Validate(cfg)
}

// warningFor returns the warning recorded for a dotted config key.
func warningFor(warnings []Warning, key string) (Warning, bool) {
	for _, w := range warnings {
		if w.Key == key {
			return w, true
		}
	}
	return Warning{}, false
}

func TestValidateInvalidModeFallsBackWithWarning(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  mode: sideways\n")

	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	w, ok := warningFor(warnings, "server.mode")
	if !ok {
		t.Fatal("expected a warning for server.mode")
	}
	if w.Value != "sideways" {
		t.Errorf("warning value = %q, want sideways", w.Value)
	}
	if !strings.Contains(w.String(), "server.mode") {
		t.Errorf("Warning.String() = %q, want it to name the key", w.String())
	}
}

func TestValidateInvalidPortPicksRandomPort(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  port: not-a-port\n")

	if cfg.Server.Port.HTTP < DefaultPortRangeMin || cfg.Server.Port.HTTP > DefaultPortRangeMax {
		t.Errorf("Port.HTTP = %d, want %d-%d", cfg.Server.Port.HTTP, DefaultPortRangeMin, DefaultPortRangeMax)
	}
	if _, ok := warningFor(warnings, "server.port"); !ok {
		t.Error("expected a warning for server.port")
	}
}

func TestValidateOutOfRangePortPicksRandomPort(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  port: 70000\n")

	if cfg.Server.Port.HTTP < DefaultPortRangeMin || cfg.Server.Port.HTTP > DefaultPortRangeMax {
		t.Errorf("Port.HTTP = %d, want a fallback port", cfg.Server.Port.HTTP)
	}
	if len(warnings) == 0 {
		t.Error("expected at least one warning for an out-of-range port")
	}
}

func TestValidatePortEightyEnablesHTTPChallenge(t *testing.T) {
	cfg, _ := validated(t, "server:\n  port: 80\n")

	if !cfg.Server.SSL.LetsEncrypt.Enabled.Value {
		t.Error("port 80 must enable Let's Encrypt")
	}
	if cfg.Server.SSL.LetsEncrypt.Challenge != "http-01" {
		t.Errorf("challenge = %q, want http-01", cfg.Server.SSL.LetsEncrypt.Challenge)
	}
}

func TestValidatePortFourFourThreeEnablesALPN(t *testing.T) {
	cfg, _ := validated(t, "server:\n  port: 443\n")

	if !cfg.Server.SSL.Enabled.Value {
		t.Error("port 443 must enable SSL")
	}
	if cfg.Server.SSL.LetsEncrypt.Challenge != "tls-alpn-01" {
		t.Errorf("challenge = %q, want tls-alpn-01", cfg.Server.SSL.LetsEncrypt.Challenge)
	}
}

func TestValidateDualPortKeepsHTTPChallenge(t *testing.T) {
	cfg, _ := validated(t, "server:\n  port: \"80,443\"\n")

	if cfg.Server.Port.HTTP != 80 || cfg.Server.Port.HTTPS != 443 {
		t.Fatalf("port = %s, want 80,443", cfg.Server.Port)
	}
	if cfg.Server.SSL.LetsEncrypt.Challenge != "http-01" {
		t.Errorf("challenge = %q, want http-01 when port 80 is also bound", cfg.Server.SSL.LetsEncrypt.Challenge)
	}
}

func TestValidateInvalidBooleanWarnsAndKeepsDefault(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  compression:\n    enabled: maybe\n")

	if !cfg.Server.Compression.Enabled.Value {
		t.Error("an unparseable boolean must keep the default (true), not flip it")
	}
	w, ok := warningFor(warnings, "server.compression.enabled")
	if !ok {
		t.Fatal("expected a warning for server.compression.enabled")
	}
	if w.Value != "maybe" {
		t.Errorf("warning value = %q, want maybe", w.Value)
	}
	if cfg.Server.Compression.Enabled.Invalid {
		t.Error("Invalid flag must be cleared once reported")
	}
}

func TestValidateInvalidDurationAndSizeWarn(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  limits:\n    read_timeout: soon\n    max_body_size: huge\n")

	if cfg.Server.Limits.ReadTimeout.Value != 30*time.Second {
		t.Errorf("read_timeout = %v, want the 30s default", cfg.Server.Limits.ReadTimeout.Value)
	}
	if cfg.Server.Limits.MaxBodySize.Value != 10<<20 {
		t.Errorf("max_body_size = %d, want the 10MB default", cfg.Server.Limits.MaxBodySize.Value)
	}
	if _, ok := warningFor(warnings, "server.limits.read_timeout"); !ok {
		t.Error("expected a warning for server.limits.read_timeout")
	}
	if _, ok := warningFor(warnings, "server.limits.max_body_size"); !ok {
		t.Error("expected a warning for server.limits.max_body_size")
	}
}

func TestValidateCompressionLevelRange(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  compression:\n    level: 42\n")

	if cfg.Server.Compression.Level != 5 {
		t.Errorf("compression.level = %d, want the default 5", cfg.Server.Compression.Level)
	}
	if _, ok := warningFor(warnings, "server.compression.level"); !ok {
		t.Error("expected a warning for server.compression.level")
	}
}

func TestValidateSessionEnumsAndIdleClamp(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  session:\n    same_site: sideways\n    secure: perhaps\n    user:\n      max_age: 1h\n      idle_timeout: 24h\n")

	if cfg.Server.Session.SameSite != "strict" {
		t.Errorf("same_site = %q, want strict", cfg.Server.Session.SameSite)
	}
	if cfg.Server.Session.Secure != "auto" {
		t.Errorf("secure = %q, want auto", cfg.Server.Session.Secure)
	}
	if cfg.Server.Session.User.IdleTimeout.Value != time.Hour {
		t.Errorf("user idle_timeout = %v, want it clamped to max_age (1h)", cfg.Server.Session.User.IdleTimeout.Value)
	}
	if _, ok := warningFor(warnings, "server.session.user.idle_timeout"); !ok {
		t.Error("expected a warning when idle_timeout exceeds max_age")
	}
}

func TestValidateRateLimitNonPositive(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  rate_limit:\n    write:\n      requests: 0\n    global_burst: -5\n")

	if cfg.Server.RateLimit.Write.Requests != 10 {
		t.Errorf("write.requests = %d, want the default 10", cfg.Server.RateLimit.Write.Requests)
	}
	if cfg.Server.RateLimit.GlobalBurst != 240 {
		t.Errorf("global_burst = %d, want the default 240", cfg.Server.RateLimit.GlobalBurst)
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one per repaired rate limit", len(warnings))
	}
}

func TestValidateGeneratesEncryptionKey(t *testing.T) {
	cfg, _ := validated(t, "server: {}\n")

	first := cfg.Server.Security.EncryptionKey
	if first == "" {
		t.Fatal("encryption_key must be generated when absent")
	}

	Validate(cfg)
	if cfg.Server.Security.EncryptionKey != first {
		t.Error("an existing valid key must never be regenerated")
	}
}

func TestValidateReplacesMalformedEncryptionKey(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  security:\n    encryption_key: \"not base64 at all\"\n")

	if cfg.Server.Security.EncryptionKey == "not base64 at all" {
		t.Error("a malformed encryption key must be replaced")
	}
	if _, ok := warningFor(warnings, "server.security.encryption_key"); !ok {
		t.Error("expected a warning for server.security.encryption_key")
	}
}

func TestValidateClusterRequiresSharedCache(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  cluster:\n    enabled: true\n  cache:\n    type: memory\n")

	if cfg.Server.Cluster.Enabled.Value {
		t.Error("cluster mode must be disabled when the cache cannot be shared")
	}
	if _, ok := warningFor(warnings, "server.cache.type"); !ok {
		t.Error("expected a warning naming server.cache.type")
	}
}

func TestValidateClusterWithValkeyKeepsClustering(t *testing.T) {
	cfg, _ := validated(t, "server:\n  cluster:\n    enabled: true\n  cache:\n    type: valkey\n")

	if !cfg.Server.Cluster.Enabled.Value {
		t.Error("valkey satisfies the shared-cache requirement")
	}
	if cfg.Server.Cluster.NodeID == "" {
		t.Error("a clustered node must be given an ID")
	}
	if !RequiresSharedCache("redis") || RequiresSharedCache("memory") {
		t.Error("RequiresSharedCache must accept redis and reject memory")
	}
}

func TestValidateUnknownDatabaseDriver(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  database:\n    driver: cassandra\n")

	if cfg.Server.Database.Driver != "sqlite" {
		t.Errorf("driver = %q, want sqlite", cfg.Server.Database.Driver)
	}
	if _, ok := warningFor(warnings, "server.database.driver"); !ok {
		t.Error("expected a warning for server.database.driver")
	}
}

func TestValidateLogLevelAndFormat(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  logs:\n    level: chatty\n    server:\n      format: yaml\n")

	if cfg.Server.Logs.Level != "warn" {
		t.Errorf("logs.level = %q, want warn", cfg.Server.Logs.Level)
	}
	if cfg.Server.Logs.Server.Format != "text" {
		t.Errorf("logs.server.format = %q, want text", cfg.Server.Logs.Server.Format)
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one for the level and one for the format", len(warnings))
	}
}

func TestValidateTrackingRequiresIDAndURL(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  tracking:\n    type: matomo\n    id: \"1\"\n")

	if cfg.Server.Tracking.Type != "" {
		t.Error("a self-hosted platform without a URL must be disabled")
	}
	if _, ok := warningFor(warnings, "server.tracking.url"); !ok {
		t.Error("expected a warning for server.tracking.url")
	}

	cfg, warnings = validated(t, "server:\n  tracking:\n    type: fathom\n")
	if cfg.Server.Tracking.Type != "" {
		t.Error("a platform without a site ID must be disabled")
	}
	if _, ok := warningFor(warnings, "server.tracking.id"); !ok {
		t.Error("expected a warning for server.tracking.id")
	}
}

func TestValidateTrackingAcceptsConfiguredPlatform(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  tracking:\n    type: Plausible\n    id: example.test\n")

	if cfg.Server.Tracking.Type != "plausible" {
		t.Errorf("tracking.type = %q, want plausible", cfg.Server.Tracking.Type)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestValidateFeatureDependencies(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  features:\n    multi_user: false\n")

	if cfg.Server.Features.Organizations.Value {
		t.Error("organizations must be disabled without multi_user")
	}
	if cfg.Server.Features.CustomDomains.Enabled.Value {
		t.Error("custom domains must be disabled without multi_user")
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one per disabled feature", len(warnings))
	}
}

func TestValidateGeoIPAllowListWins(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  geoip:\n    allow_countries: [us, ca]\n    deny_countries: [ru]\n")

	if len(cfg.Server.GeoIP.DenyCountries) != 0 {
		t.Errorf("deny_countries = %v, want it cleared", cfg.Server.GeoIP.DenyCountries)
	}
	if len(cfg.Server.GeoIP.AllowCountries) != 2 || cfg.Server.GeoIP.AllowCountries[0] != "US" {
		t.Errorf("allow_countries = %v, want [US CA]", cfg.Server.GeoIP.AllowCountries)
	}
	if _, ok := warningFor(warnings, "server.geoip.deny_countries"); !ok {
		t.Error("expected a warning for the ignored deny list")
	}
}

func TestValidateUpdateBranchAndDefer(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  update:\n    branch: nightly\n    defer_days: 900\n")

	if cfg.Server.Update.Branch != "stable" {
		t.Errorf("update.branch = %q, want stable", cfg.Server.Update.Branch)
	}
	if cfg.Server.Update.DeferDays != 0 {
		t.Errorf("defer_days = %d, want 0", cfg.Server.Update.DeferDays)
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one per repaired update setting", len(warnings))
	}
}

func TestValidateRegistrationAndOrgModes(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  users:\n    registration:\n      mode: whenever\n    roles:\n      default: wizard\n  orgs:\n    creation:\n      mode: whenever\n")

	if cfg.Server.Users.Registration.Mode != "open" {
		t.Errorf("registration.mode = %q, want open", cfg.Server.Users.Registration.Mode)
	}
	if cfg.Server.Orgs.Creation.Mode != "open" {
		t.Errorf("orgs.creation.mode = %q, want open", cfg.Server.Orgs.Creation.Mode)
	}
	if cfg.Server.Users.Roles.Default != "admin" {
		t.Errorf("roles.default = %q, want the first available role", cfg.Server.Users.Roles.Default)
	}
	if len(warnings) < 3 {
		t.Errorf("got %d warnings, want one per repaired mode", len(warnings))
	}
}

func TestValidateI2PTunnelRanges(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  i2p:\n    inbound_length: 12\n    outbound_quantity: 0\n")

	if cfg.Server.I2P.InboundLength != 3 {
		t.Errorf("inbound_length = %d, want 3", cfg.Server.I2P.InboundLength)
	}
	if cfg.Server.I2P.OutboundQuantity != 5 {
		t.Errorf("outbound_quantity = %d, want 5", cfg.Server.I2P.OutboundQuantity)
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one per out-of-range tunnel setting", len(warnings))
	}
}

func TestValidateAdminPathAndBaseURL(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  admin_path: some/where\n  baseurl: panel\n")

	if cfg.Server.AdminPath != DefaultAdminPath {
		t.Errorf("admin_path = %q, want %q", cfg.Server.AdminPath, DefaultAdminPath)
	}
	if cfg.Server.BaseURL != "/panel/" {
		t.Errorf("baseurl = %q, want /panel/", cfg.Server.BaseURL)
	}
	if _, ok := warningFor(warnings, "server.admin_path"); !ok {
		t.Error("expected a warning for server.admin_path")
	}
}

func TestValidateEssentialCookiesCannotBeDisabled(t *testing.T) {
	cfg, warnings := validated(t, "server:\n  privacy:\n    cookies:\n      essential:\n        enabled: false\n    consent:\n      position: middle\n")

	if !cfg.Server.Privacy.Cookies.Essential.Enabled.Value {
		t.Error("essential cookies must stay enabled")
	}
	if cfg.Server.Privacy.Consent.Position != "bottom" {
		t.Errorf("consent.position = %q, want bottom", cfg.Server.Privacy.Consent.Position)
	}
	if len(warnings) < 2 {
		t.Errorf("got %d warnings, want one per repaired privacy setting", len(warnings))
	}
}

func TestValidateNilConfigIsSafe(t *testing.T) {
	if warnings := Validate(nil); warnings != nil {
		t.Errorf("Validate(nil) = %v, want nil", warnings)
	}
}

func TestRandomAvailablePortIsUsable(t *testing.T) {
	port, err := RandomAvailablePort()
	if err != nil {
		t.Fatalf("RandomAvailablePort() error = %v", err)
	}
	if port < DefaultPortRangeMin || port > DefaultPortRangeMax {
		t.Errorf("port = %d, want %d-%d", port, DefaultPortRangeMin, DefaultPortRangeMax)
	}
	if !portAvailable(port) {
		t.Errorf("port %d reported as taken immediately after selection", port)
	}
}

func TestParsePort(t *testing.T) {
	if p, err := parsePort("8080"); err != nil || p != 8080 {
		t.Errorf("parsePort(8080) = %d, %v", p, err)
	}
	if _, err := parsePort("70000"); err == nil {
		t.Error("parsePort(70000) should reject an out-of-range port")
	}
	if _, err := parsePort("http"); err == nil {
		t.Error("parsePort(http) should reject a non-numeric port")
	}
}
