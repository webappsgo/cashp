package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"strconv"
	"strings"
)

// Warning is one repaired configuration value. AI.md PART 12's config
// validation rule is absolute: an invalid setting is replaced with its
// documented default and reported, never a reason to fail startup.
type Warning struct {
	// Key is the dotted server.yml path of the offending setting.
	Key string
	// Value is the rejected value as the operator wrote it.
	Value string
	// Message explains the replacement that was applied.
	Message string
}

// String renders the warning as a single log line.
func (w Warning) String() string {
	if w.Value == "" {
		return fmt.Sprintf("config: %s: %s", w.Key, w.Message)
	}
	return fmt.Sprintf("config: %s: invalid value %q: %s", w.Key, w.Value, w.Message)
}

// Validate repairs cfg in place and returns the list of substitutions it
// made. It never returns an error and never leaves cfg unusable: every
// invalid value is replaced with the documented default from Defaults().
func Validate(cfg *Config) []Warning {
	if cfg == nil {
		return nil
	}

	def := Defaults()
	var warnings []Warning

	warnings = append(warnings, collectUnparsed(reflect.ValueOf(cfg).Elem(), "")...)
	warnings = append(warnings, validateServerCore(cfg, def)...)
	warnings = append(warnings, validatePort(cfg)...)
	warnings = append(warnings, validateLimits(cfg, def)...)
	warnings = append(warnings, validateSession(cfg, def)...)
	warnings = append(warnings, validateRateLimit(cfg, def)...)
	warnings = append(warnings, validateSecurity(cfg, def)...)
	warnings = append(warnings, validateCache(cfg, def)...)
	warnings = append(warnings, validateDatabase(cfg, def)...)
	warnings = append(warnings, validateLogs(cfg, def)...)
	warnings = append(warnings, validateContact(cfg)...)
	warnings = append(warnings, validatePrivacy(cfg, def)...)
	warnings = append(warnings, validateTracking(cfg)...)
	warnings = append(warnings, validateFeatures(cfg)...)
	warnings = append(warnings, validateGeoIP(cfg)...)
	warnings = append(warnings, validateUpdate(cfg, def)...)
	warnings = append(warnings, validateI2P(cfg, def)...)

	cfg.proxies = newProxyResolver(cfg.Server.TrustedProxy.Additional, cfg.Server.Address)

	return warnings
}

// unparsedTypes lists the scalar wrappers that record a failed decode
// instead of aborting it, so the reflection walk can report them uniformly.
var (
	boolType     = reflect.TypeOf(Bool{})
	durationType = reflect.TypeOf(Duration{})
	sizeType     = reflect.TypeOf(Size{})
	portType     = reflect.TypeOf(PortSpec{})
)

// collectUnparsed walks the config tree and reports every scalar that could
// not be decoded, clearing the flag so the retained default stands alone.
func collectUnparsed(v reflect.Value, path string) []Warning {
	var warnings []Warning

	switch v.Type() {
	case boolType, durationType, sizeType, portType:
		if !v.FieldByName("Invalid").Bool() {
			return nil
		}
		raw := v.FieldByName("Raw").String()
		v.FieldByName("Invalid").SetBool(false)
		v.FieldByName("Raw").SetString("")
		return []Warning{{Key: path, Value: raw, Message: "not a valid " + scalarKind(v.Type()) + ", keeping the default"}}
	}

	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			warnings = append(warnings, collectUnparsed(v.Field(i), joinPath(path, yamlName(field)))...)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			warnings = append(warnings, collectUnparsed(v.Index(i), path+"["+strconv.Itoa(i)+"]")...)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			entry := reflect.New(v.Type().Elem()).Elem()
			entry.Set(v.MapIndex(key))
			sub := collectUnparsed(entry, joinPath(path, fmt.Sprint(key.Interface())))
			if len(sub) > 0 {
				v.SetMapIndex(key, entry)
				warnings = append(warnings, sub...)
			}
		}
	case reflect.Pointer:
		if !v.IsNil() {
			warnings = append(warnings, collectUnparsed(v.Elem(), path)...)
		}
	}

	return warnings
}

// scalarKind names a wrapper type for warning messages.
func scalarKind(t reflect.Type) string {
	switch t {
	case boolType:
		return "boolean"
	case durationType:
		return "duration"
	case sizeType:
		return "size"
	default:
		return "port"
	}
}

// yamlName returns the server.yml key a struct field maps to, falling back
// to the lowercased Go name when no tag is present.
func yamlName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	name := strings.Split(tag, ",")[0]
	if name == "" || name == "-" {
		if strings.Contains(tag, ",inline") {
			return ""
		}
		return strings.ToLower(field.Name)
	}
	return name
}

// joinPath appends a key to a dotted config path, skipping empty segments
// produced by inlined structs.
func joinPath(base, key string) string {
	switch {
	case key == "":
		return base
	case base == "":
		return key
	default:
		return base + "." + key
	}
}

// validateServerCore repairs mode, baseurl, admin path, and API version.
func validateServerCore(cfg, def *Config) []Warning {
	var warnings []Warning
	s := &cfg.Server

	switch s.Mode {
	case "production", "development", "debug":
	case "":
		s.Mode = def.Server.Mode
	default:
		warnings = append(warnings, Warning{"server.mode", s.Mode, "expected production, development, or debug; using " + def.Server.Mode})
		s.Mode = def.Server.Mode
	}

	if s.Address == "" {
		s.Address = def.Server.Address
	}

	normalized := NormalizeBaseURL(s.BaseURL)
	if normalized != s.BaseURL && s.BaseURL != "" {
		warnings = append(warnings, Warning{"server.baseurl", s.BaseURL, "normalized to " + normalized})
	}
	s.BaseURL = normalized

	if s.AdminPath == "" || strings.ContainsAny(s.AdminPath, "/ ") {
		if s.AdminPath != "" {
			warnings = append(warnings, Warning{"server.admin_path", s.AdminPath, "must be a single path segment; using " + DefaultAdminPath})
		}
		s.AdminPath = DefaultAdminPath
	}

	if s.APIVersion == "" {
		s.APIVersion = DefaultAPIVersion
	}

	if s.Branding.Title == "" {
		s.Branding.Title = def.Server.Branding.Title
	}

	return warnings
}

// validatePort repairs the listen port and applies the special-port
// behavior: 80 turns on the HTTP-01 challenge, 443 turns on TLS-ALPN-01 and
// auto SSL. An unset port is assigned a random unused 64000-64999 port.
func validatePort(cfg *Config) []Warning {
	var warnings []Warning
	s := &cfg.Server

	if s.Port.HTTP == 0 && s.Port.Raw != "0" {
		port, err := RandomAvailablePort()
		if err != nil {
			return append(warnings, Warning{"server.port", "", "no free port in 64000-64999: " + err.Error()})
		}
		if s.Port.Raw != "" {
			warnings = append(warnings, Warning{"server.port", s.Port.Raw, "using random port " + strconv.Itoa(port)})
		}
		s.Port = NewPortSpec(port)
	}

	if s.Port.HTTPS == s.Port.HTTP && s.Port.HTTPS != 0 {
		warnings = append(warnings, Warning{"server.port", s.Port.String(), "HTTP and HTTPS ports must differ; dropping the HTTPS port"})
		s.Port.HTTPS = 0
		s.Port.Raw = strconv.Itoa(s.Port.HTTP)
	}

	if s.Port.HTTP == 80 || s.Port.HTTPS == 80 {
		s.SSL.LetsEncrypt.Enabled = NewBool(true)
		s.SSL.LetsEncrypt.Challenge = "http-01"
	}

	if s.Port.HTTP == 443 || s.Port.HTTPS == 443 {
		s.SSL.Enabled = NewBool(true)
		s.SSL.LetsEncrypt.Enabled = NewBool(true)
		if s.Port.HTTP != 80 && s.Port.HTTPS != 80 {
			s.SSL.LetsEncrypt.Challenge = "tls-alpn-01"
		}
	}

	switch s.SSL.MinVersion {
	case "TLS1.2", "TLS1.3":
	case "":
		s.SSL.MinVersion = "TLS1.2"
	default:
		warnings = append(warnings, Warning{"server.ssl.min_version", s.SSL.MinVersion, "expected TLS1.2 or TLS1.3; using TLS1.2"})
		s.SSL.MinVersion = "TLS1.2"
	}

	switch s.SSL.LetsEncrypt.Challenge {
	case "http-01", "tls-alpn-01", "dns-01":
	case "":
		s.SSL.LetsEncrypt.Challenge = "http-01"
	default:
		warnings = append(warnings, Warning{"server.ssl.letsencrypt.challenge", s.SSL.LetsEncrypt.Challenge, "expected http-01, tls-alpn-01, or dns-01; using http-01"})
		s.SSL.LetsEncrypt.Challenge = "http-01"
	}

	return warnings
}

// validateLimits repairs non-positive timeouts, body size, and compression
// level.
func validateLimits(cfg, def *Config) []Warning {
	var warnings []Warning
	l := &cfg.Server.Limits
	d := def.Server.Limits

	for _, item := range []struct {
		key   string
		field *Duration
		def   Duration
	}{
		{"server.limits.read_timeout", &l.ReadTimeout, d.ReadTimeout},
		{"server.limits.write_timeout", &l.WriteTimeout, d.WriteTimeout},
		{"server.limits.idle_timeout", &l.IdleTimeout, d.IdleTimeout},
	} {
		if item.field.Value <= 0 {
			warnings = append(warnings, Warning{item.key, FormatDuration(item.field.Value), "must be positive; using " + FormatDuration(item.def.Value)})
			*item.field = item.def
		}
	}

	if l.MaxBodySize.Value <= 0 {
		warnings = append(warnings, Warning{"server.limits.max_body_size", FormatSize(l.MaxBodySize.Value), "must be positive; using " + FormatSize(d.MaxBodySize.Value)})
		l.MaxBodySize = d.MaxBodySize
	}

	if c := &cfg.Server.Compression; c.Level < 1 || c.Level > 9 {
		warnings = append(warnings, Warning{"server.compression.level", strconv.Itoa(c.Level), "must be 1-9; using " + strconv.Itoa(def.Server.Compression.Level)})
		c.Level = def.Server.Compression.Level
	}

	if len(cfg.Server.Compression.Types) == 0 {
		cfg.Server.Compression.Types = def.Server.Compression.Types
	}

	return warnings
}

// validateSession repairs cookie names, lifetimes, and the SameSite/secure
// enums.
func validateSession(cfg, def *Config) []Warning {
	var warnings []Warning
	s := &cfg.Server.Session
	d := def.Server.Session

	for _, item := range []struct {
		key      string
		audience *SessionAudienceConfig
		def      SessionAudienceConfig
	}{
		{"server.session.admin", &s.Admin, d.Admin},
		{"server.session.user", &s.User, d.User},
	} {
		if item.audience.CookieName == "" {
			item.audience.CookieName = item.def.CookieName
		}
		if item.audience.MaxAge.Value <= 0 {
			warnings = append(warnings, Warning{item.key + ".max_age", FormatDuration(item.audience.MaxAge.Value), "must be positive; using " + FormatDuration(item.def.MaxAge.Value)})
			item.audience.MaxAge = item.def.MaxAge
		}
		if item.audience.IdleTimeout.Value <= 0 {
			warnings = append(warnings, Warning{item.key + ".idle_timeout", FormatDuration(item.audience.IdleTimeout.Value), "must be positive; using " + FormatDuration(item.def.IdleTimeout.Value)})
			item.audience.IdleTimeout = item.def.IdleTimeout
		}
		if item.audience.IdleTimeout.Value > item.audience.MaxAge.Value {
			warnings = append(warnings, Warning{item.key + ".idle_timeout", FormatDuration(item.audience.IdleTimeout.Value), "cannot exceed max_age; using " + FormatDuration(item.audience.MaxAge.Value)})
			item.audience.IdleTimeout = item.audience.MaxAge
		}
	}

	switch strings.ToLower(s.SameSite) {
	case "strict", "lax", "none":
		s.SameSite = strings.ToLower(s.SameSite)
	case "":
		s.SameSite = d.SameSite
	default:
		warnings = append(warnings, Warning{"server.session.same_site", s.SameSite, "expected strict, lax, or none; using strict"})
		s.SameSite = d.SameSite
	}

	switch strings.ToLower(s.Secure) {
	case "auto", "true", "false":
		s.Secure = strings.ToLower(s.Secure)
	case "":
		s.Secure = d.Secure
	default:
		warnings = append(warnings, Warning{"server.session.secure", s.Secure, "expected auto, true, or false; using auto"})
		s.Secure = d.Secure
	}

	return warnings
}

// validateRateLimit repairs non-positive request counts and windows.
func validateRateLimit(cfg, def *Config) []Warning {
	var warnings []Warning
	r := &cfg.Server.RateLimit
	d := def.Server.RateLimit

	for _, item := range []struct {
		key  string
		rule *RateLimitRule
		def  RateLimitRule
	}{
		{"server.rate_limit.read", &r.Read, d.Read},
		{"server.rate_limit.write", &r.Write, d.Write},
		{"server.rate_limit.health", &r.Health, d.Health},
		{"server.rate_limit.auth.login", &r.Auth.Login, d.Auth.Login},
		{"server.rate_limit.auth.password_reset", &r.Auth.PasswordReset, d.Auth.PasswordReset},
		{"server.rate_limit.auth.registration", &r.Auth.Registration, d.Auth.Registration},
	} {
		if item.rule.Requests <= 0 {
			warnings = append(warnings, Warning{item.key + ".requests", strconv.Itoa(item.rule.Requests), "must be positive; using " + strconv.Itoa(item.def.Requests)})
			item.rule.Requests = item.def.Requests
		}
		if item.rule.Window <= 0 {
			warnings = append(warnings, Warning{item.key + ".window", strconv.Itoa(item.rule.Window), "must be positive; using " + strconv.Itoa(item.def.Window)})
			item.rule.Window = item.def.Window
		}
	}

	if r.GlobalBurst <= 0 {
		warnings = append(warnings, Warning{"server.rate_limit.global_burst", strconv.Itoa(r.GlobalBurst), "must be positive; using " + strconv.Itoa(d.GlobalBurst)})
		r.GlobalBurst = d.GlobalBurst
	}

	return warnings
}

// validateSecurity generates the at-rest encryption key on first run and
// repairs a malformed one, and repairs the breach detection thresholds.
func validateSecurity(cfg, def *Config) []Warning {
	var warnings []Warning
	s := &cfg.Server.Security
	d := def.Server.Security

	if s.EncryptionKey == "" {
		key, err := GenerateEncryptionKey()
		if err != nil {
			warnings = append(warnings, Warning{"server.security.encryption_key", "", "could not generate a key: " + err.Error()})
		} else {
			s.EncryptionKey = key
		}
	} else if raw, err := base64.StdEncoding.DecodeString(s.EncryptionKey); err != nil || len(raw) != EncryptionKeySize {
		key, genErr := GenerateEncryptionKey()
		if genErr != nil {
			warnings = append(warnings, Warning{"server.security.encryption_key", "", "could not regenerate a key: " + genErr.Error()})
		} else {
			s.EncryptionKey = key
			warnings = append(warnings, Warning{"server.security.encryption_key", "", "not a base64 32-byte key; generated a new one (previously encrypted data must be re-entered)"})
		}
	}

	if s.EncryptionKeyVersion < 1 {
		s.EncryptionKeyVersion = d.EncryptionKeyVersion
	}

	b := &s.BreachDetection
	db := d.BreachDetection

	if b.BruteForce.Attempts <= 0 {
		warnings = append(warnings, Warning{"server.security.breach_detection.brute_force.attempts", strconv.Itoa(b.BruteForce.Attempts), "must be positive; using " + strconv.Itoa(db.BruteForce.Attempts)})
		b.BruteForce.Attempts = db.BruteForce.Attempts
	}
	if b.BruteForce.Window.Value <= 0 {
		b.BruteForce.Window = db.BruteForce.Window
	}
	if b.BruteForce.BlockDuration.Value <= 0 {
		b.BruteForce.BlockDuration = db.BruteForce.BlockDuration
	}
	if b.CredentialStuffing.Attempts <= 0 {
		warnings = append(warnings, Warning{"server.security.breach_detection.credential_stuffing.attempts", strconv.Itoa(b.CredentialStuffing.Attempts), "must be positive; using " + strconv.Itoa(db.CredentialStuffing.Attempts)})
		b.CredentialStuffing.Attempts = db.CredentialStuffing.Attempts
	}
	if b.CredentialStuffing.Window.Value <= 0 {
		b.CredentialStuffing.Window = db.CredentialStuffing.Window
	}

	return warnings
}

// EncryptionKeySize is the byte length of server.security.encryption_key,
// sized for AES-256-GCM.
const EncryptionKeySize = 32

// GenerateEncryptionKey returns a fresh base64-encoded 32-byte key for
// server.security.encryption_key.
func GenerateEncryptionKey() (string, error) {
	buf := make([]byte, EncryptionKeySize)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// validateCache repairs the cache backend selection and connection
// settings. Cluster and mixed mode require valkey or redis; when they are
// paired with the memory backend the cluster is disabled rather than left
// in a state where nodes silently disagree.
func validateCache(cfg, def *Config) []Warning {
	var warnings []Warning
	c := &cfg.Server.Cache
	d := def.Server.Cache

	switch strings.ToLower(c.Type) {
	case "none", "memory", "valkey", "redis":
		c.Type = strings.ToLower(c.Type)
	case "":
		c.Type = d.Type
	default:
		warnings = append(warnings, Warning{"server.cache.type", c.Type, "expected none, memory, valkey, or redis; using " + d.Type})
		c.Type = d.Type
	}

	if c.Port < 1 || c.Port > 65535 {
		warnings = append(warnings, Warning{"server.cache.port", strconv.Itoa(c.Port), "out of range; using " + strconv.Itoa(d.Port)})
		c.Port = d.Port
	}
	if c.Host == "" {
		c.Host = d.Host
	}
	if c.DB < 0 {
		c.DB = d.DB
	}
	if c.PoolSize < 1 {
		c.PoolSize = d.PoolSize
	}
	if c.MinIdle < 0 || c.MinIdle > c.PoolSize {
		c.MinIdle = d.MinIdle
	}
	if c.Timeout.Value <= 0 {
		c.Timeout = d.Timeout
	}
	if c.TTL.Value <= 0 {
		c.TTL = d.TTL
	}
	if c.Prefix == "" {
		c.Prefix = d.Prefix
	}
	if c.Cluster.Value && len(c.ClusterNodes) == 0 {
		warnings = append(warnings, Warning{"server.cache.cluster_nodes", "", "cache cluster mode needs at least one node; using the single host/port connection"})
		c.Cluster = NewBool(false)
	}

	if clusterMode(cfg) && !RequiresSharedCache(c.Type) {
		warnings = append(warnings, Warning{"server.cache.type", c.Type, "cluster and mixed mode require valkey or redis; disabling cluster mode and running single-instance"})
		cfg.Server.Cluster.Enabled = NewBool(false)
		cfg.Server.Cluster.MixedMode = NewBool(false)
	}

	if cfg.Server.Cluster.Enabled.Value && cfg.Server.Cluster.NodeID == "" {
		cfg.Server.Cluster.NodeID = LocalNodeID()
	}
	if cfg.Server.Cluster.HeartbeatInterval.Value <= 0 {
		cfg.Server.Cluster.HeartbeatInterval = def.Server.Cluster.HeartbeatInterval
	}

	return warnings
}

// clusterMode reports whether cfg asks for any multi-node behavior.
func clusterMode(cfg *Config) bool {
	return cfg.Server.Cluster.Enabled.Value || cfg.Server.Cluster.MixedMode.Value
}

// RequiresSharedCache reports whether a cache backend can carry cluster
// state: only valkey and redis are shared across nodes.
func RequiresSharedCache(cacheType string) bool {
	switch strings.ToLower(cacheType) {
	case "valkey", "redis":
		return true
	default:
		return false
	}
}

// validateDatabase repairs the database driver selection.
func validateDatabase(cfg, def *Config) []Warning {
	var warnings []Warning
	db := &cfg.Server.Database

	switch strings.ToLower(db.Driver) {
	case "sqlite", "postgres", "mysql", "mssql", "mongodb":
		db.Driver = strings.ToLower(db.Driver)
	case "":
		db.Driver = def.Server.Database.Driver
	default:
		warnings = append(warnings, Warning{"server.database.driver", db.Driver, "unsupported driver; using " + def.Server.Database.Driver})
		db.Driver = def.Server.Database.Driver
	}

	if db.Driver == "sqlite" && db.Dir == "" {
		db.Dir = def.Server.Database.Dir
	}

	return warnings
}

// validateLogs repairs the global level and each log's format.
func validateLogs(cfg, def *Config) []Warning {
	var warnings []Warning
	l := &cfg.Server.Logs

	switch strings.ToLower(l.Level) {
	case "debug", "info", "warn", "error":
		l.Level = strings.ToLower(l.Level)
	case "":
		l.Level = def.Server.Logs.Level
	default:
		warnings = append(warnings, Warning{"server.logs.level", l.Level, "expected debug, info, warn, or error; using " + def.Server.Logs.Level})
		l.Level = def.Server.Logs.Level
	}

	if l.Dir == "" {
		l.Dir = def.Server.Logs.Dir
	}

	if !validLogFormat(l.Access.Format, "apache", "nginx", "json", "custom") {
		warnings = append(warnings, Warning{"server.logs.access.format", l.Access.Format, "expected apache, nginx, json, or custom; using apache"})
		l.Access.Format = def.Server.Logs.Access.Format
	}

	for _, item := range []struct {
		key  string
		file *LogFile
		def  LogFile
	}{
		{"server.logs.server", &l.Server, def.Server.Logs.Server},
		{"server.logs.error", &l.Error, def.Server.Logs.Error},
		{"server.logs.security", &l.Security, def.Server.Logs.Security},
		{"server.logs.scheduler", &l.Scheduler, def.Server.Logs.Scheduler},
	} {
		if !validLogFormat(item.file.Format, "text", "json", "custom") {
			warnings = append(warnings, Warning{item.key + ".format", item.file.Format, "expected text, json, or custom; using " + item.def.Format})
			item.file.Format = item.def.Format
		}
		if item.file.Filename == "" {
			item.file.Filename = item.def.Filename
		}
	}

	if strings.ToLower(l.Audit.Format) != "json" {
		if l.Audit.Format != "" {
			warnings = append(warnings, Warning{"server.logs.audit.format", l.Audit.Format, "the audit log must stay machine-parseable; using json"})
		}
		l.Audit.Format = "json"
	}

	return warnings
}

// validLogFormat reports whether format is one of the allowed values,
// treating an empty format as invalid so the default is restored.
func validLogFormat(format string, allowed ...string) bool {
	for _, a := range allowed {
		if strings.EqualFold(format, a) {
			return true
		}
	}
	return false
}

// validateContact warns when the universal fallback recipient is missing
// and ensures every role has a webhook map to write into.
func validateContact(cfg *Config) []Warning {
	var warnings []Warning
	c := &cfg.Server.Contact

	if c.Admin.Email == "" {
		warnings = append(warnings, Warning{"server.contact.admin.email", "", "no admin recipient configured; server notifications will not be delivered"})
	}

	for _, role := range []*ContactRole{&c.Admin, &c.Security, &c.Abuse, &c.General} {
		if role.Webhooks == nil {
			role.Webhooks = map[string]string{}
		}
	}

	return warnings
}

// validatePrivacy repairs the consent banner enums and restores the banner
// copy when an operator blanks it out.
func validatePrivacy(cfg, def *Config) []Warning {
	var warnings []Warning
	p := &cfg.Server.Privacy
	d := def.Server.Privacy

	switch strings.ToLower(p.Consent.Position) {
	case "bottom", "top":
		p.Consent.Position = strings.ToLower(p.Consent.Position)
	case "":
		p.Consent.Position = d.Consent.Position
	default:
		warnings = append(warnings, Warning{"server.privacy.consent.position", p.Consent.Position, "expected bottom or top; using bottom"})
		p.Consent.Position = d.Consent.Position
	}

	if p.Consent.Message == "" {
		p.Consent.Message = d.Consent.Message
	}
	if p.Consent.MessageIfSold == "" {
		p.Consent.MessageIfSold = d.Consent.MessageIfSold
	}
	if p.Consent.Buttons.Accept == "" {
		p.Consent.Buttons.Accept = d.Consent.Buttons.Accept
	}
	if p.Consent.Buttons.Decline == "" {
		p.Consent.Buttons.Decline = d.Consent.Buttons.Decline
	}
	if p.Consent.Policy.URL == "" {
		p.Consent.Policy = d.Consent.Policy
	}
	if p.Consent.CookieName == "" {
		p.Consent.CookieName = d.Consent.CookieName
	}

	if !p.Cookies.Essential.Enabled.Value {
		warnings = append(warnings, Warning{"server.privacy.cookies.essential.enabled", "false", "essential cookies cannot be disabled; forcing true"})
		p.Cookies.Essential.Enabled = NewBool(true)
	}

	return warnings
}

// trackingTypes lists the analytics platforms with built-in script
// generation, mapped to whether a self-hosted URL is mandatory.
var trackingTypes = map[string]bool{
	"google":     false,
	"matomo":     true,
	"piwik":      true,
	"owa":        true,
	"fathom":     false,
	"plausible":  false,
	"umami":      true,
	"simple":     false,
	"cloudflare": false,
}

// validateTracking repairs the analytics platform selection and disables
// tracking that cannot work as configured.
func validateTracking(cfg *Config) []Warning {
	var warnings []Warning
	t := &cfg.Server.Tracking

	t.Type = strings.ToLower(strings.TrimSpace(t.Type))
	if t.Type == "none" {
		t.Type = ""
	}
	if t.Type == "" {
		return warnings
	}

	needsURL, known := trackingTypes[t.Type]
	if !known {
		warnings = append(warnings, Warning{"server.tracking.type", t.Type, "unknown analytics platform; disabling tracking"})
		t.Type = ""
		return warnings
	}

	if t.ID == "" {
		warnings = append(warnings, Warning{"server.tracking.id", "", "no site ID for " + t.Type + "; disabling tracking"})
		t.Type = ""
		return warnings
	}

	if needsURL && t.URL == "" {
		warnings = append(warnings, Warning{"server.tracking.url", "", t.Type + " requires a self-hosted URL; disabling tracking"})
		t.Type = ""
	}

	return warnings
}

// validateFeatures enforces the dependency order between feature toggles:
// organizations and custom domains both need multi-user accounts.
func validateFeatures(cfg *Config) []Warning {
	var warnings []Warning
	f := &cfg.Server.Features

	if !f.MultiUser.Value {
		if f.Organizations.Value {
			warnings = append(warnings, Warning{"server.features.organizations", "true", "organizations require multi_user; disabling organizations"})
			f.Organizations = NewBool(false)
		}
		if f.CustomDomains.Enabled.Value {
			warnings = append(warnings, Warning{"server.features.custom_domains.enabled", "true", "custom domains require multi_user; disabling custom domains"})
			f.CustomDomains.Enabled = NewBool(false)
		}
	}

	cfg.Server.Users.Enabled = f.MultiUser
	cfg.Server.Orgs.Enabled = f.Organizations

	if f.CustomDomains.MaxDomainsPerUser < 0 {
		f.CustomDomains.MaxDomainsPerUser = 0
	}
	if f.CustomDomains.MaxDomainsPerOrg < 0 {
		f.CustomDomains.MaxDomainsPerOrg = 0
	}

	warnings = append(warnings, validateModes(cfg)...)

	if cfg.Tor.OnionAddress != "" && !strings.HasSuffix(cfg.Tor.OnionAddress, ".onion") {
		warnings = append(warnings, Warning{"tor.onion_address", cfg.Tor.OnionAddress, "not a .onion hostname; Tor request detection stays disabled"})
		cfg.Tor.OnionAddress = ""
	}

	return warnings
}

// validateModes repairs the registration and organization creation modes.
func validateModes(cfg *Config) []Warning {
	var warnings []Warning

	switch strings.ToLower(cfg.Server.Users.Registration.Mode) {
	case "open", "invite", "admin_only", "disabled":
		cfg.Server.Users.Registration.Mode = strings.ToLower(cfg.Server.Users.Registration.Mode)
	case "":
		cfg.Server.Users.Registration.Mode = "open"
	default:
		warnings = append(warnings, Warning{"server.users.registration.mode", cfg.Server.Users.Registration.Mode, "expected open, invite, admin_only, or disabled; using open"})
		cfg.Server.Users.Registration.Mode = "open"
	}

	switch strings.ToLower(cfg.Server.Orgs.Creation.Mode) {
	case "open", "invite", "admin_only", "disabled":
		cfg.Server.Orgs.Creation.Mode = strings.ToLower(cfg.Server.Orgs.Creation.Mode)
	case "":
		cfg.Server.Orgs.Creation.Mode = "open"
	default:
		warnings = append(warnings, Warning{"server.orgs.creation.mode", cfg.Server.Orgs.Creation.Mode, "expected open, invite, admin_only, or disabled; using open"})
		cfg.Server.Orgs.Creation.Mode = "open"
	}

	if cfg.Server.Users.Registration.InviteExpirationDays <= 0 {
		cfg.Server.Users.Registration.InviteExpirationDays = 7
	}

	if cfg.Server.Users.Roles.Default == "" {
		cfg.Server.Users.Roles.Default = "user"
	}
	if len(cfg.Server.Users.Roles.Available) == 0 {
		cfg.Server.Users.Roles.Available = []string{"admin", "user"}
	}
	if !containsFold(cfg.Server.Users.Roles.Available, cfg.Server.Users.Roles.Default) {
		warnings = append(warnings, Warning{"server.users.roles.default", cfg.Server.Users.Roles.Default, "not in roles.available; using " + cfg.Server.Users.Roles.Available[0]})
		cfg.Server.Users.Roles.Default = cfg.Server.Users.Roles.Available[0]
	}

	return warnings
}

// containsFold reports whether list holds value, ignoring case.
func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// validateGeoIP normalizes country codes and resolves the deny/allow
// conflict in favor of the allow-list, as the spec requires.
func validateGeoIP(cfg *Config) []Warning {
	var warnings []Warning
	g := &cfg.Server.GeoIP

	g.DenyCountries = normalizeCountries(g.DenyCountries)
	g.AllowCountries = normalizeCountries(g.AllowCountries)

	if len(g.AllowCountries) > 0 && len(g.DenyCountries) > 0 {
		warnings = append(warnings, Warning{"server.geoip.deny_countries", strings.Join(g.DenyCountries, ","), "allow_countries takes precedence; ignoring deny_countries"})
		g.DenyCountries = nil
	}

	if g.Dir == "" {
		g.Dir = Defaults().Server.GeoIP.Dir
	}

	return warnings
}

// normalizeCountries upper-cases entries and drops anything that is not an
// ISO 3166-1 alpha-2 code.
func normalizeCountries(codes []string) []string {
	var out []string
	for _, code := range codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if len(code) != 2 {
			continue
		}
		out = append(out, code)
	}
	return out
}

// validateUpdate repairs the release channel and defer window.
func validateUpdate(cfg, def *Config) []Warning {
	var warnings []Warning
	u := &cfg.Server.Update

	switch strings.ToLower(u.Branch) {
	case "stable", "beta", "daily":
		u.Branch = strings.ToLower(u.Branch)
	case "":
		u.Branch = def.Server.Update.Branch
	default:
		warnings = append(warnings, Warning{"server.update.branch", u.Branch, "expected stable, beta, or daily; using stable"})
		u.Branch = def.Server.Update.Branch
	}

	if u.DeferDays < 0 || u.DeferDays > 365 {
		warnings = append(warnings, Warning{"server.update.defer_days", strconv.Itoa(u.DeferDays), "must be 0-365; using 0"})
		u.DeferDays = 0
	}

	if b := &cfg.Server.Backup.Retention; b.MaxBackups < 1 {
		warnings = append(warnings, Warning{"server.backup.retention.max_backups", strconv.Itoa(b.MaxBackups), "must be at least 1; using 1"})
		b.MaxBackups = 1
	}

	if cfg.Server.Compliance.Enabled.Value && !cfg.Server.Backup.Encryption.Enabled.Value {
		warnings = append(warnings, Warning{"server.backup.encryption.enabled", "false", "compliance mode requires encrypted backups; backups will be skipped until a password is set"})
	}

	if m := &cfg.Server.Maintenance.Cleanup; m.DiskThreshold < 1 || m.DiskThreshold > 100 {
		warnings = append(warnings, Warning{"server.maintenance.cleanup.disk_threshold", strconv.Itoa(m.DiskThreshold), "must be 1-100; using 90"})
		m.DiskThreshold = 90
	}

	return warnings
}

// validateI2P repairs the eepsite tunnel parameters, which have hard ranges
// imposed by the I2P router.
func validateI2P(cfg, def *Config) []Warning {
	var warnings []Warning
	i := &cfg.Server.I2P
	d := def.Server.I2P

	for _, item := range []struct {
		key   string
		field *int
		def   int
		min   int
		max   int
	}{
		{"server.i2p.inbound_length", &i.InboundLength, d.InboundLength, 0, 7},
		{"server.i2p.outbound_length", &i.OutboundLength, d.OutboundLength, 0, 7},
		{"server.i2p.inbound_quantity", &i.InboundQuantity, d.InboundQuantity, 1, 16},
		{"server.i2p.outbound_quantity", &i.OutboundQuantity, d.OutboundQuantity, 1, 16},
		{"server.i2p.virtual_port", &i.VirtualPort, d.VirtualPort, 1, 65535},
	} {
		if *item.field < item.min || *item.field > item.max {
			warnings = append(warnings, Warning{item.key, strconv.Itoa(*item.field), fmt.Sprintf("must be %d-%d; using %d", item.min, item.max, item.def)})
			*item.field = item.def
		}
	}

	if i.SAMAddress == "" {
		i.SAMAddress = d.SAMAddress
	}
	if i.BootstrapTimeout.Value <= 0 {
		i.BootstrapTimeout = d.BootstrapTimeout
	}

	return warnings
}

// RandomAvailablePort returns an unused TCP port in the 64000-64999 range.
// The range avoids the well-known ports every other service competes for
// and needs no elevated privileges.
func RandomAvailablePort() (int, error) {
	span := int64(DefaultPortRangeMax - DefaultPortRangeMin + 1)

	for attempt := 0; attempt < 100; attempt++ {
		n, err := rand.Int(rand.Reader, big.NewInt(span))
		if err != nil {
			return 0, err
		}

		port := DefaultPortRangeMin + int(n.Int64())
		if portAvailable(port) {
			return port, nil
		}
	}

	for port := DefaultPortRangeMin; port <= DefaultPortRangeMax; port++ {
		if portAvailable(port) {
			return port, nil
		}
	}

	return 0, fmt.Errorf("config: no free port in %d-%d", DefaultPortRangeMin, DefaultPortRangeMax)
}

// portAvailable reports whether a TCP listener can bind port on all
// interfaces right now.
func portAvailable(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
