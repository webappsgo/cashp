// Package config loads, validates, and persists cashp's server
// configuration per AI.md PART 5, PART 6, and PART 12. server.yml is the
// single accepted config file name (case-sensitive, never server.yaml —
// auto-migrated on startup); YAML comments are single-line, above the
// setting, under 140 characters.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the full server.yml document. Every key AI.md PART 12 defines
// has a typed field here; Defaults() documents the default for each one.
type Config struct {
	Server ServerConfig `yaml:"server"`
	Web    WebConfig    `yaml:"web"`
	Tor    TorConfig    `yaml:"tor"`

	// proxies caches the resolved trusted-proxy matcher built from
	// Server.TrustedProxy. Validate installs it; it is never serialized.
	proxies *proxyResolver
}

// ServerConfig is everything under the top-level `server:` key.
type ServerConfig struct {
	Mode          string              `yaml:"mode"`
	Address       string              `yaml:"address"`
	Port          PortSpec            `yaml:"port"`
	FQDN          string              `yaml:"fqdn"`
	BaseURL       string              `yaml:"baseurl"`
	AdminPath     string              `yaml:"admin_path"`
	APIVersion    string              `yaml:"api_version"`
	User          string              `yaml:"user"`
	Group         string              `yaml:"group"`
	PIDFile       Bool                `yaml:"pidfile"`
	Daemonize     Bool                `yaml:"daemonize"`
	Healthz       HealthzConfig       `yaml:"healthz"`
	Branding      BrandingConfig      `yaml:"branding"`
	SEO           SEOConfig           `yaml:"seo"`
	Admin         AdminConfig         `yaml:"admin"`
	SSL           SSLConfig           `yaml:"ssl"`
	Limits        LimitsConfig        `yaml:"limits"`
	Compression   CompressionConfig   `yaml:"compression"`
	TrustedProxy  TrustedProxyConfig  `yaml:"trusted_proxies"`
	Session       SessionConfig       `yaml:"session"`
	RateLimit     RateLimitConfig     `yaml:"rate_limit"`
	Security      SecurityConfig      `yaml:"security"`
	I18n          I18nConfig          `yaml:"i18n"`
	Contact       ContactConfig       `yaml:"contact"`
	Tracking      TrackingConfig      `yaml:"tracking"`
	Privacy       PrivacyConfig       `yaml:"privacy"`
	Cache         CacheConfig         `yaml:"cache"`
	Database      DatabaseConfig      `yaml:"database"`
	Cluster       ClusterConfig       `yaml:"cluster"`
	Logs          LogsConfig          `yaml:"logs"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
	Backup        BackupConfig        `yaml:"backup"`
	Compliance    ComplianceConfig    `yaml:"compliance"`
	Update        UpdateConfig        `yaml:"update"`
	Metrics       MetricsConfig       `yaml:"metrics"`
	GeoIP         GeoIPConfig         `yaml:"geoip"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Maintenance   MaintenanceConfig   `yaml:"maintenance"`
	Features      FeaturesConfig      `yaml:"features"`
	Users         UsersConfig         `yaml:"users"`
	Orgs          OrgsConfig          `yaml:"orgs"`
	I2P           I2PConfig           `yaml:"i2p"`
}

// HealthzConfig controls the optional root /healthz compatibility alias.
// The canonical route is always /server/healthz.
type HealthzConfig struct {
	Root RootAliasConfig `yaml:"root"`
}

// RootAliasConfig is the shared shape of the "also mount this at the site
// root" toggles (health checks, metrics).
type RootAliasConfig struct {
	Enabled Bool `yaml:"enabled"`
}

// BrandingConfig holds the public-facing product identity strings.
type BrandingConfig struct {
	Title       string `yaml:"title"`
	Tagline     string `yaml:"tagline"`
	Description string `yaml:"description"`
}

// SEOConfig holds search-engine metadata rendered into page heads.
type SEOConfig struct {
	Keywords []string `yaml:"keywords"`
}

// AdminConfig holds the admin panel settings kept in server.yml. Username,
// password, and tokens live in the database, never in this file.
type AdminConfig struct {
	Email string `yaml:"email"`
}

// SSLConfig holds TLS settings. Empty cert/key means auto-detection walks
// the letsencrypt and local certificate directories described in PART 5.
type SSLConfig struct {
	Enabled      Bool               `yaml:"enabled"`
	Cert         string             `yaml:"cert"`
	Key          string             `yaml:"key"`
	MinVersion   string             `yaml:"min_version"`
	LetsEncrypt  LetsEncryptConfig  `yaml:"letsencrypt"`
	RedirectHTTP Bool               `yaml:"redirect_http"`
	HSTS         HSTSConfig         `yaml:"hsts"`
}

// LetsEncryptConfig holds ACME settings. Port 80 selects http-01 and port
// 443 selects tls-alpn-01 automatically on first run.
type LetsEncryptConfig struct {
	Enabled   Bool   `yaml:"enabled"`
	Email     string `yaml:"email"`
	Challenge string `yaml:"challenge"`
	Staging   Bool   `yaml:"staging"`
}

// HSTSConfig holds Strict-Transport-Security emission settings. HSTS is
// never sent on Tor or I2P responses regardless of these values.
type HSTSConfig struct {
	Enabled           Bool     `yaml:"enabled"`
	MaxAge            Duration `yaml:"max_age"`
	IncludeSubdomains Bool     `yaml:"include_subdomains"`
	Preload           Bool     `yaml:"preload"`
}

// LimitsConfig holds request size and timeout limits.
type LimitsConfig struct {
	MaxBodySize  Size     `yaml:"max_body_size"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	IdleTimeout  Duration `yaml:"idle_timeout"`
}

// CompressionConfig holds response compression settings.
type CompressionConfig struct {
	Enabled Bool     `yaml:"enabled"`
	Level   int      `yaml:"level"`
	Types   []string `yaml:"types"`
}

// TrustedProxyConfig holds the operator-supplied additions to the always
// trusted private ranges. Entries may be IPs, CIDRs, or DNS names.
type TrustedProxyConfig struct {
	Additional []string `yaml:"additional"`
}

// SessionConfig holds cookie session settings shared by admin and user
// sessions plus the per-audience lifetimes.
type SessionConfig struct {
	Admin            SessionAudienceConfig `yaml:"admin"`
	User             SessionAudienceConfig `yaml:"user"`
	ExtendOnActivity Bool                  `yaml:"extend_on_activity"`
	Secure           string                `yaml:"secure"`
	HTTPOnly         Bool                  `yaml:"http_only"`
	SameSite         string                `yaml:"same_site"`
}

// SessionAudienceConfig is one session audience: admin sessions live in
// server.db, user sessions in users.db.
type SessionAudienceConfig struct {
	CookieName  string   `yaml:"cookie_name"`
	MaxAge      Duration `yaml:"max_age"`
	IdleTimeout Duration `yaml:"idle_timeout"`
}

// RateLimitConfig holds the per-IP sliding-window limits.
type RateLimitConfig struct {
	Enabled     Bool                `yaml:"enabled"`
	Read        RateLimitRule       `yaml:"read"`
	Write       RateLimitRule       `yaml:"write"`
	Health      RateLimitRule       `yaml:"health"`
	GlobalBurst int                 `yaml:"global_burst"`
	Auth        RateLimitAuthConfig `yaml:"auth"`
}

// RateLimitRule is one sliding-window limit: requests allowed per window
// seconds, per IP.
type RateLimitRule struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

// RateLimitAuthConfig holds the stricter auth-endpoint limits, applied
// independently of the general read/write limits.
type RateLimitAuthConfig struct {
	Login         RateLimitRule `yaml:"login"`
	PasswordReset RateLimitRule `yaml:"password_reset"`
	Registration  RateLimitRule `yaml:"registration"`
}

// SecurityConfig holds the at-rest encryption key and breach detection
// thresholds. Breach detection is always on; only thresholds are tunable.
type SecurityConfig struct {
	EncryptionKey        string                `yaml:"encryption_key"`
	EncryptionKeyVersion int                   `yaml:"encryption_key_version"`
	Allowlist            []string              `yaml:"allowlist"`
	Blocklist            []string              `yaml:"blocklist"`
	BreachDetection      BreachDetectionConfig `yaml:"breach_detection"`
}

// BreachDetectionConfig holds the automated breach detection thresholds.
type BreachDetectionConfig struct {
	BruteForce          BruteForceConfig          `yaml:"brute_force"`
	CredentialStuffing  CredentialStuffingConfig  `yaml:"credential_stuffing"`
	UnusualAccess       UnusualAccessConfig       `yaml:"unusual_access"`
}

// BruteForceConfig thresholds repeated failed logins from a single IP.
type BruteForceConfig struct {
	Attempts      int      `yaml:"attempts"`
	Window        Duration `yaml:"window"`
	BlockDuration Duration `yaml:"block_duration"`
}

// CredentialStuffingConfig thresholds failed logins spread across accounts.
type CredentialStuffingConfig struct {
	Attempts int      `yaml:"attempts"`
	Window   Duration `yaml:"window"`
}

// UnusualAccessConfig controls new-country and anomaly alerting.
type UnusualAccessConfig struct {
	NewCountryAlert Bool `yaml:"new_country_alert"`
}

// I18nConfig holds language settings.
type I18nConfig struct {
	DefaultLanguage string   `yaml:"default_language"`
	Supported       []string `yaml:"supported"`
}

// ContactConfig is the unified "where do messages go" tree. admin is the
// universal fallback for every other role.
type ContactConfig struct {
	Admin    ContactRole `yaml:"admin"`
	Security ContactRole `yaml:"security"`
	Abuse    ContactRole `yaml:"abuse"`
	General  ContactRole `yaml:"general"`
}

// ContactRole is one notification recipient: an email plus any number of
// named webhook transports. Webhook URLs are never publicly exposed.
type ContactRole struct {
	Email    string            `yaml:"email"`
	Webhooks map[string]string `yaml:"webhooks"`
}

// TrackingConfig holds the server-wide analytics platform settings.
type TrackingConfig struct {
	Type string `yaml:"type"`
	ID   string `yaml:"id"`
	URL  string `yaml:"url"`
}

// PrivacyConfig holds data handling policy, retention, consent banner, and
// cookie category settings.
type PrivacyConfig struct {
	Data       PrivacyDataConfig       `yaml:"data"`
	Retention  PrivacyRetentionConfig  `yaml:"retention"`
	Consent    ConsentConfig           `yaml:"consent"`
	Cookies    CookieCategoriesConfig  `yaml:"cookies"`
	ThirdParty PrivacyThirdPartyConfig `yaml:"third_party"`
	Content    PrivacyContentConfig    `yaml:"content"`
}

// PrivacyDataConfig declares whether data is sold and where it is stored.
type PrivacyDataConfig struct {
	Sold            Bool                  `yaml:"sold"`
	StoredOnServer  Bool                  `yaml:"stored_on_server"`
	Sharing         []PrivacySharingEntry `yaml:"sharing"`
}

// PrivacySharingEntry describes one condition under which data may be
// shared with a third party.
type PrivacySharingEntry struct {
	Condition string `yaml:"condition"`
	When      string `yaml:"when"`
	Data      string `yaml:"data"`
}

// PrivacyRetentionConfig describes how long data is kept and which
// self-service data rights are offered.
type PrivacyRetentionConfig struct {
	Period            string `yaml:"period"`
	ExportAvailable   Bool   `yaml:"export_available"`
	DeletionAvailable Bool   `yaml:"deletion_available"`
}

// ConsentConfig holds the cookie consent banner settings. The model is
// opt-out: non-essential cookies are enabled until the visitor declines.
type ConsentConfig struct {
	ShowUntilAcknowledged Bool                 `yaml:"show_until_acknowledged"`
	DefaultEnabled        Bool                 `yaml:"default_enabled"`
	Message               string               `yaml:"message"`
	MessageIfSold         string               `yaml:"message_if_sold"`
	Policy                ConsentPolicyConfig  `yaml:"policy"`
	Buttons               ConsentButtonsConfig `yaml:"buttons"`
	Position              string               `yaml:"position"`
	ShowPreferences       Bool                 `yaml:"show_preferences"`
	PreferencesText       string               `yaml:"preferences_text"`
	CookieName            string               `yaml:"cookie_name"`
}

// ConsentPolicyConfig is the privacy policy link shown in the banner.
type ConsentPolicyConfig struct {
	Text string `yaml:"text"`
	URL  string `yaml:"url"`
}

// ConsentButtonsConfig holds the banner button labels.
type ConsentButtonsConfig struct {
	Decline string `yaml:"decline"`
	Accept  string `yaml:"accept"`
}

// CookieCategoriesConfig holds the three consent categories. Essential
// cookies are always enabled and cannot be turned off.
type CookieCategoriesConfig struct {
	Essential   CookieCategory          `yaml:"essential"`
	Preferences CookieCategory          `yaml:"preferences"`
	Analytics   AnalyticsCookieCategory `yaml:"analytics"`
}

// CookieCategory is one consent category with its user-facing description.
type CookieCategory struct {
	Enabled     Bool   `yaml:"enabled"`
	Description string `yaml:"description"`
}

// AnalyticsCookieCategory extends CookieCategory with the description
// suffixes chosen dynamically from privacy.data.sold.
type AnalyticsCookieCategory struct {
	Enabled                  Bool   `yaml:"enabled"`
	Description              string `yaml:"description"`
	DescriptionSuffixNotSold string `yaml:"description_suffix_not_sold"`
	DescriptionSuffixSold    string `yaml:"description_suffix_sold"`
}

// PrivacyThirdPartyConfig lists third-party services, auto-populated from
// the tracking config plus manual entries.
type PrivacyThirdPartyConfig struct {
	Services []ThirdPartyService `yaml:"services"`
}

// ThirdPartyService is one disclosed third-party recipient of user data.
type ThirdPartyService struct {
	Name      string `yaml:"name"`
	Purpose   string `yaml:"purpose"`
	DataSent  string `yaml:"data_sent"`
	PolicyURL string `yaml:"policy_url"`
}

// PrivacyContentConfig holds the Markdown bodies for the privacy page. An
// empty section falls back to the built-in template copy at render time.
type PrivacyContentConfig struct {
	DataCollection string `yaml:"data_collection"`
	DataUsage      string `yaml:"data_usage"`
	UserRights     string `yaml:"user_rights"`
	Contact        string `yaml:"contact"`
}

// CacheConfig holds cache settings. valkey or redis is REQUIRED for cluster
// and mixed mode; memory only works for a single instance.
type CacheConfig struct {
	Type          string   `yaml:"type"`
	URL           string   `yaml:"url"`
	Host          string   `yaml:"host"`
	Port          int      `yaml:"port"`
	Username      string   `yaml:"username"`
	Password      string   `yaml:"password"`
	DB            int      `yaml:"db"`
	TLS           Bool     `yaml:"tls"`
	TLSSkipVerify Bool     `yaml:"tls_skip_verify"`
	PoolSize      int      `yaml:"pool_size"`
	MinIdle       int      `yaml:"min_idle"`
	Timeout       Duration `yaml:"timeout"`
	Prefix        string   `yaml:"prefix"`
	TTL           Duration `yaml:"ttl"`
	Cluster       Bool     `yaml:"cluster"`
	ClusterNodes  []string `yaml:"cluster_nodes"`
}

// DatabaseConfig holds the database connection settings. In single-instance
// mode this is SQLite under DataDir(); in cluster mode it points at the
// shared remote database that becomes the source of truth.
type DatabaseConfig struct {
	Driver   string `yaml:"driver"`
	URL      string `yaml:"url,omitempty"`
	Dir      string `yaml:"dir,omitempty"`
	Host     string `yaml:"host,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	Name     string `yaml:"name,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
	SSLMode  string `yaml:"sslmode,omitempty"`
}

// ClusterConfig holds multi-node settings. When enabled the remote database
// becomes the configuration source of truth and server.yml caches a
// read-only fallback copy.
type ClusterConfig struct {
	Enabled           Bool     `yaml:"enabled"`
	NodeID            string   `yaml:"node_id"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
	MixedMode         Bool     `yaml:"mixed_mode"`
}

// LogsConfig holds the global log level and per-log-type policies.
type LogsConfig struct {
	Level     string        `yaml:"level"`
	Dir       string        `yaml:"dir"`
	Access    AccessLogFile `yaml:"access"`
	Server    LogFile       `yaml:"server"`
	Error     LogFile       `yaml:"error"`
	Security  LogFile       `yaml:"security"`
	Scheduler LogFile       `yaml:"scheduler"`
	Audit     AuditLogFile  `yaml:"audit"`
}

// LogFile is the shared per-log policy: filename, format, rotation, and
// retention.
type LogFile struct {
	Enabled  Bool   `yaml:"enabled"`
	Filename string `yaml:"filename"`
	Format   string `yaml:"format"`
	Custom   string `yaml:"custom"`
	Rotate   string `yaml:"rotate"`
	Keep     string `yaml:"keep"`
	Compress Bool   `yaml:"compress"`
}

// AccessLogFile extends LogFile with the health-check logging toggle;
// failed health checks are always logged regardless.
type AccessLogFile struct {
	LogFile         `yaml:",inline"`
	LogHealthChecks Bool `yaml:"log_health_checks"`
}

// AuditLogFile extends LogFile with the audit event category switches.
type AuditLogFile struct {
	LogFile `yaml:",inline"`
	Events  AuditEvents `yaml:"events"`
}

// AuditEvents selects which audit event categories are recorded.
type AuditEvents struct {
	Authentication Bool `yaml:"authentication"`
	Configuration  Bool `yaml:"configuration"`
	Security       Bool `yaml:"security"`
	Tokens         Bool `yaml:"tokens"`
	DataAccess     Bool `yaml:"data_access"`
}

// SchedulerConfig holds the background task scheduler settings.
type SchedulerConfig struct {
	Enabled Bool                     `yaml:"enabled"`
	Tasks   map[string]ScheduledTask `yaml:"tasks"`
}

// ScheduledTask is one scheduler entry. Schedule is cron syntax or a
// descriptor such as @hourly.
type ScheduledTask struct {
	Enabled     Bool     `yaml:"enabled"`
	Schedule    string   `yaml:"schedule"`
	RetryOnFail Bool     `yaml:"retry_on_fail"`
	RetryDelay  Duration `yaml:"retry_delay"`
	Retention   int      `yaml:"retention,omitempty"`
	RenewBefore Duration `yaml:"renew_before,omitempty"`
}

// BackupConfig holds backup destination, retention, and encryption
// settings. The backup password is never stored, only prompted.
type BackupConfig struct {
	Enabled    Bool                   `yaml:"enabled"`
	Dir        string                 `yaml:"dir"`
	Retention  BackupRetentionConfig  `yaml:"retention"`
	Encryption BackupEncryptionConfig `yaml:"encryption"`
}

// BackupRetentionConfig holds the count and size caps for retained
// backups. A size cap of "0" disables the cap.
type BackupRetentionConfig struct {
	MaxBackups   int    `yaml:"max_backups"`
	KeepWeekly   int    `yaml:"keep_weekly"`
	KeepMonthly  int    `yaml:"keep_monthly"`
	KeepYearly   int    `yaml:"keep_yearly"`
	MaxTotalSize string `yaml:"max_total_size"`
}

// BackupEncryptionConfig records whether an encryption password has been
// set. The password itself is never persisted.
type BackupEncryptionConfig struct {
	Enabled Bool `yaml:"enabled"`
}

// ComplianceConfig enables regulated-deployment enforcement; when on,
// backups refuse to run without an encryption password.
type ComplianceConfig struct {
	Enabled Bool `yaml:"enabled"`
}

// UpdateConfig holds the self-update channel and adoption policy.
type UpdateConfig struct {
	Branch      string `yaml:"branch"`
	AutoInstall Bool   `yaml:"auto_install"`
	DeferDays   int    `yaml:"defer_days"`
}

// MetricsConfig holds the Prometheus-compatible metrics endpoint settings.
type MetricsConfig struct {
	Enabled         Bool              `yaml:"enabled"`
	Root            RootAliasConfig   `yaml:"root"`
	Auth            MetricsAuthConfig `yaml:"auth"`
	IncludeSystem   Bool              `yaml:"include_system"`
	IncludeRuntime  Bool              `yaml:"include_runtime"`
	Loki            MetricsLokiConfig `yaml:"loki"`
	DurationBuckets []float64         `yaml:"duration_buckets"`
	SizeBuckets     []float64         `yaml:"size_buckets"`
}

// MetricsAuthConfig holds per-service bearer tokens. An empty token
// disables that service with a 403.
type MetricsAuthConfig struct {
	AllowUnauthenticated Bool              `yaml:"allow_unauthenticated"`
	Tokens               map[string]string `yaml:"tokens"`
}

// MetricsLokiConfig bounds how much recent log the Loki service serves.
type MetricsLokiConfig struct {
	MaxEntries int      `yaml:"max_entries"`
	MaxAge     Duration `yaml:"max_age"`
}

// GeoIPConfig holds MMDB download settings and country blocking rules.
// allow_countries wins when both lists are populated.
type GeoIPConfig struct {
	Enabled        Bool                 `yaml:"enabled"`
	Dir            string               `yaml:"dir"`
	DenyCountries  []string             `yaml:"deny_countries"`
	AllowCountries []string             `yaml:"allow_countries"`
	Databases      GeoIPDatabasesConfig `yaml:"databases"`
}

// GeoIPDatabasesConfig selects which MMDB databases to download and use.
type GeoIPDatabasesConfig struct {
	ASN     Bool `yaml:"asn"`
	Country Bool `yaml:"country"`
	City    Bool `yaml:"city"`
}

// NotificationsConfig holds outbound notification transports. Email is the
// only transport configured in server.yml; webhooks live under contact.
type NotificationsConfig struct {
	Email EmailConfig `yaml:"email"`
}

// EmailConfig holds SMTP delivery settings. An empty host triggers local
// MTA autodetection at startup.
type EmailConfig struct {
	Enabled Bool           `yaml:"enabled"`
	SMTP    SMTPConfig     `yaml:"smtp"`
	From    EmailFromConfig `yaml:"from"`
}

// SMTPConfig holds the SMTP connection settings. SMTP_* environment
// variables override these on every load.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	TLS      string `yaml:"tls"`
}

// EmailFromConfig holds the envelope sender identity. Empty values resolve
// to the app title and no-reply@{fqdn} at send time.
type EmailFromConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// MaintenanceConfig holds self-healing, cleanup, and notification settings
// for maintenance mode.
type MaintenanceConfig struct {
	SelfHealing SelfHealingConfig        `yaml:"self_healing"`
	Cleanup     MaintenanceCleanupConfig `yaml:"cleanup"`
	Notify      MaintenanceNotifyConfig  `yaml:"notify"`
}

// SelfHealingConfig controls automatic recovery retries. max_attempts 0
// means keep retrying forever.
type SelfHealingConfig struct {
	Enabled       Bool     `yaml:"enabled"`
	RetryInterval Duration `yaml:"retry_interval"`
	MaxAttempts   int      `yaml:"max_attempts"`
}

// MaintenanceCleanupConfig holds the disk-pressure cleanup thresholds.
type MaintenanceCleanupConfig struct {
	DiskThreshold    int `yaml:"disk_threshold"`
	LogRetentionDays int `yaml:"log_retention_days"`
	BackupKeepCount  int `yaml:"backup_keep_count"`
}

// MaintenanceNotifyConfig controls maintenance-mode transition alerts.
type MaintenanceNotifyConfig struct {
	OnEnter Bool `yaml:"on_enter"`
	OnExit  Bool `yaml:"on_exit"`
}

// FeaturesConfig holds the project feature toggles. cashp is a multi-user
// project with organizations and custom domains enabled.
type FeaturesConfig struct {
	MultiUser     Bool                `yaml:"multi_user"`
	Organizations Bool                `yaml:"organizations"`
	CustomDomains CustomDomainsConfig `yaml:"custom_domains"`
	Tor           Bool                `yaml:"tor"`
	I2P           Bool                `yaml:"i2p"`
	Registration  Bool                `yaml:"registration"`
	APITokens     Bool                `yaml:"api_tokens"`
}

// CustomDomainsConfig holds the bring-your-own-domain limits. A limit of 0
// means unlimited.
type CustomDomainsConfig struct {
	Enabled           Bool `yaml:"enabled"`
	MaxDomainsPerUser int  `yaml:"max_domains_per_user"`
	MaxDomainsPerOrg  int  `yaml:"max_domains_per_org"`
	RequireSSL        Bool `yaml:"require_ssl"`
	AllowApex         Bool `yaml:"allow_apex"`
}

// UsersConfig holds end-user account settings.
type UsersConfig struct {
	Enabled      Bool               `yaml:"enabled"`
	Registration RegistrationConfig `yaml:"registration"`
	Roles        RolesConfig        `yaml:"roles"`
	Tokens       UserTokensConfig   `yaml:"tokens"`
}

// RegistrationConfig holds account creation rules. Mode is one of open,
// invite, admin_only, or disabled.
type RegistrationConfig struct {
	Mode                     string   `yaml:"mode"`
	RequireEmailVerification Bool     `yaml:"require_email_verification"`
	AllowedDomains           []string `yaml:"allowed_domains"`
	BlockedDomains           []string `yaml:"blocked_domains"`
	InviteExpirationDays     int      `yaml:"invite_expiration_days"`
}

// RolesConfig holds the available roles, the default assigned to new
// accounts, and any custom role permission sets.
type RolesConfig struct {
	Available   []string            `yaml:"available"`
	Default     string              `yaml:"default"`
	Permissions map[string][]string `yaml:"permissions"`
}

// UserTokensConfig controls user-generated API tokens.
type UserTokensConfig struct {
	Enabled Bool `yaml:"enabled"`
	MaxPerUser int `yaml:"max_per_user"`
}

// OrgsConfig holds organization/team settings.
type OrgsConfig struct {
	Enabled  Bool                `yaml:"enabled"`
	Creation OrgCreationConfig   `yaml:"creation"`
	Profile  OrgProfileConfig    `yaml:"profile"`
	Members  OrgMembersConfig    `yaml:"members"`
}

// OrgCreationConfig holds who may create organizations. Mode is one of
// open, invite, admin_only, or disabled.
type OrgCreationConfig struct {
	Mode string `yaml:"mode"`
}

// OrgProfileConfig holds organization profile defaults.
type OrgProfileConfig struct {
	DefaultVisibility string `yaml:"default_visibility"`
}

// OrgMembersConfig holds organization membership defaults.
type OrgMembersConfig struct {
	DefaultRole  string `yaml:"default_role"`
	Require2FA   Bool   `yaml:"require_2fa"`
	AllowInvites Bool   `yaml:"allow_invites"`
}

// I2PConfig holds eepsite settings. I2P is opt-in: no provider is contacted
// and no port allocated unless enabled is true.
type I2PConfig struct {
	Enabled          Bool     `yaml:"enabled"`
	Binary           string   `yaml:"binary"`
	SAMAddress       string   `yaml:"sam_address"`
	VirtualPort      int      `yaml:"virtual_port"`
	InboundLength    int      `yaml:"inbound_length"`
	OutboundLength   int      `yaml:"outbound_length"`
	InboundQuantity  int      `yaml:"inbound_quantity"`
	OutboundQuantity int      `yaml:"outbound_quantity"`
	SignatureType    int      `yaml:"signature_type"`
	BootstrapTimeout Duration `yaml:"bootstrap_timeout"`
	B32Address       string   `yaml:"b32_address"`
}

// TorConfig holds hidden service settings. An empty onion_address disables
// Tor request detection entirely.
type TorConfig struct {
	Enabled      Bool   `yaml:"enabled"`
	OnionAddress string `yaml:"onion_address"`
	ContactEmail string `yaml:"contact_email"`
}

// WebConfig is everything under the top-level `web:` key.
type WebConfig struct {
	UI   WebUIConfig `yaml:"ui"`
	CORS string      `yaml:"cors"`
}

// WebUIConfig holds frontend presentation defaults.
type WebUIConfig struct {
	Theme string `yaml:"theme"`
}

// Load reads server.yml from ConfigFilePath(), migrating a legacy
// server.yaml first, applying init-only environment overrides on first run
// and runtime overrides on every load, then validating. Validation never
// fails startup: invalid values are replaced with defaults and reported as
// warnings via LoadWithWarnings. A missing config file is not an error.
func Load() (*Config, error) {
	cfg, _, err := LoadWithWarnings()
	return cfg, err
}

// LoadWithWarnings is Load plus the validation warnings produced while
// repairing invalid values. The error return is reserved for genuinely
// unusable input: an unreadable or unparseable config file.
func LoadWithWarnings() (*Config, []Warning, error) {
	cfg := Defaults()

	if err := MigrateLegacyConfig(); err != nil {
		return nil, nil, err
	}

	firstRun := true
	path := ConfigFilePath()
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		firstRun = false
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, nil, err
		}
	case os.IsNotExist(err):
	default:
		return nil, nil, err
	}

	if firstRun {
		applyInitOnlyEnv(cfg)
	}
	applyRuntimeEnv(cfg)

	// A config that predates the encryption key gains one here, so the
	// generated key must be written back even though this is not a first run.
	keyMissing := cfg.Server.Security.EncryptionKey == ""

	warnings := Validate(cfg)

	if firstRun || keyMissing {
		if err := Save(cfg); err != nil {
			return nil, nil, err
		}
	}

	return cfg, warnings, nil
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

// LegacyConfigFilePath returns the path of the pre-standard server.yaml
// that MigrateLegacyConfig renames to server.yml.
func LegacyConfigFilePath() string {
	return filepath.Join(ConfigDir(), "server.yaml")
}

// MigrateLegacyConfig renames a legacy server.yaml to server.yml. It is a
// no-op when the legacy file is absent or when server.yml already exists,
// so an operator who kept both files never loses the canonical one.
func MigrateLegacyConfig() error {
	legacy := LegacyConfigFilePath()
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}

	current := ConfigFilePath()
	if _, err := os.Stat(current); err == nil {
		return nil
	}

	return os.Rename(legacy, current)
}

// applyInitOnlyEnv applies environment variables that are read once, on
// first run, and ignored afterward (AI.md PART 5 "Init-Only Variables").
func applyInitOnlyEnv(cfg *Config) {
	if v := os.Getenv("LISTEN"); v != "" {
		cfg.Server.Address = v
	}
	if v := os.Getenv("PORT"); v != "" {
		if spec, err := ParsePortSpec(v); err == nil {
			cfg.Server.Port = spec
		}
	}
	if v := os.Getenv("APPLICATION_NAME"); v != "" {
		cfg.Server.Branding.Title = v
	}
	if v := os.Getenv("APPLICATION_TAGLINE"); v != "" {
		cfg.Server.Branding.Tagline = v
	}
	if v := os.Getenv("DATABASE_DIR"); v != "" {
		cfg.Server.Database.Dir = v
	}
	if v := os.Getenv("BACKUP_DIR"); v != "" {
		cfg.Server.Backup.Dir = v
	}
	if v := os.Getenv("LOG_DIR"); v != "" {
		cfg.Server.Logs.Dir = v
	}
}

// applyRuntimeEnv applies environment variables that are checked on every
// load, always taking priority over server.yml (AI.md PART 5 "Runtime
// Variables").
func applyRuntimeEnv(cfg *Config) {
	if v := os.Getenv("MODE"); v != "" {
		cfg.Server.Mode = v
	}
	if v := os.Getenv("DOMAIN"); v != "" {
		cfg.Server.FQDN = v
	}
	if v := os.Getenv("DATABASE_DRIVER"); v != "" {
		cfg.Server.Database.Driver = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Server.Database.URL = v
	}

	applySMTPEnv(cfg)
}

// applySMTPEnv applies the SMTP_* runtime overrides, which exist so
// containers can supply mail credentials without rewriting server.yml.
func applySMTPEnv(cfg *Config) {
	smtp := &cfg.Server.Notifications.Email.SMTP

	if v := os.Getenv("SMTP_HOST"); v != "" {
		smtp.Host = v
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if p, err := parsePort(v); err == nil {
			smtp.Port = p
		}
	}
	if v := os.Getenv("SMTP_USERNAME"); v != "" {
		smtp.Username = v
	}
	if v := os.Getenv("SMTP_PASSWORD"); v != "" {
		smtp.Password = v
	}
	if v := os.Getenv("SMTP_TLS"); v != "" {
		smtp.TLS = v
	}
	if v := os.Getenv("SMTP_FROM_NAME"); v != "" {
		cfg.Server.Notifications.Email.From.Name = v
	}
	if v := os.Getenv("SMTP_FROM_EMAIL"); v != "" {
		cfg.Server.Notifications.Email.From.Email = v
	}
}
