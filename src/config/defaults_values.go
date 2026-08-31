package config

import (
	"path/filepath"
	"time"
)

// DefaultAdminPath is the admin panel path segment mounted under
// /server/{admin_path}/ (AI.md PART 5 example structure, PART 17).
const DefaultAdminPath = "administration"

// DefaultAPIVersion is the API prefix used in /api/{api_version}/ routes.
const DefaultAPIVersion = "v1"

// DefaultConsentCookieName is the cookie holding the visitor's ConsentState
// JSON. Consent is never stored in localStorage.
const DefaultConsentCookieName = "cookie_consent"

// Defaults returns a Config populated with every documented default from
// AI.md PART 5 and PART 12. Load() starts from this and overlays server.yml,
// so any key the operator omits keeps the value set here.
func Defaults() *Config {
	cfg := &Config{}

	cfg.Server = ServerConfig{
		Mode:       "production",
		Address:    "[::]",
		Port:       PortSpec{},
		AdminPath:  DefaultAdminPath,
		APIVersion: DefaultAPIVersion,
		BaseURL:    "/",
		PIDFile:    NewBool(true),
		Daemonize:  NewBool(false),
	}

	cfg.Server.Healthz.Root.Enabled = NewBool(false)
	cfg.Server.Branding = BrandingConfig{Title: InternalName}

	defaultSSL(&cfg.Server.SSL)
	defaultLimits(&cfg.Server.Limits)
	defaultCompression(&cfg.Server.Compression)
	defaultSession(&cfg.Server.Session)
	defaultRateLimit(&cfg.Server.RateLimit)
	defaultSecurity(&cfg.Server.Security)
	defaultContact(&cfg.Server.Contact)
	defaultPrivacy(&cfg.Server.Privacy)
	defaultCache(&cfg.Server.Cache)
	defaultLogs(&cfg.Server.Logs)
	defaultScheduler(&cfg.Server.Scheduler)
	defaultBackup(&cfg.Server.Backup)
	defaultMetrics(&cfg.Server.Metrics)
	defaultGeoIP(&cfg.Server.GeoIP)
	defaultNotifications(&cfg.Server.Notifications)
	defaultMaintenance(&cfg.Server.Maintenance)
	defaultFeatures(&cfg.Server.Features)
	defaultUsers(&cfg.Server.Users)
	defaultOrgs(&cfg.Server.Orgs)
	defaultI2P(&cfg.Server.I2P)

	cfg.Server.I18n = I18nConfig{DefaultLanguage: "en", Supported: []string{"en"}}
	cfg.Server.Database = DatabaseConfig{Driver: "sqlite", Dir: DataDir()}
	cfg.Server.Cluster = ClusterConfig{
		Enabled:           NewBool(false),
		HeartbeatInterval: NewDuration(30 * time.Second),
		MixedMode:         NewBool(false),
	}
	cfg.Server.Compliance = ComplianceConfig{Enabled: NewBool(false)}
	cfg.Server.Update = UpdateConfig{Branch: "stable", AutoInstall: NewBool(false)}

	cfg.Tor = TorConfig{Enabled: NewBool(false)}
	cfg.Web = WebConfig{UI: WebUIConfig{Theme: "dark"}, CORS: "*"}

	return cfg
}

// defaultSSL applies the TLS defaults. SSL is off until the operator turns
// it on or a port of 443 auto-enables it during validation.
func defaultSSL(c *SSLConfig) {
	*c = SSLConfig{
		Enabled:      NewBool(false),
		MinVersion:   "TLS1.2",
		RedirectHTTP: NewBool(false),
		LetsEncrypt: LetsEncryptConfig{
			Enabled:   NewBool(false),
			Challenge: "http-01",
			Staging:   NewBool(false),
		},
		HSTS: HSTSConfig{
			Enabled:           NewBool(false),
			MaxAge:            NewDuration(365 * 24 * time.Hour),
			IncludeSubdomains: NewBool(false),
			Preload:           NewBool(false),
		},
	}
}

// defaultLimits applies the request size and timeout defaults.
func defaultLimits(c *LimitsConfig) {
	*c = LimitsConfig{
		MaxBodySize:  NewSize(10 << 20),
		ReadTimeout:  NewDuration(30 * time.Second),
		WriteTimeout: NewDuration(30 * time.Second),
		IdleTimeout:  NewDuration(120 * time.Second),
	}
}

// defaultCompression applies the response compression defaults.
func defaultCompression(c *CompressionConfig) {
	*c = CompressionConfig{
		Enabled: NewBool(true),
		Level:   5,
		Types: []string{
			"text/html",
			"text/css",
			"text/javascript",
			"application/json",
			"application/xml",
		},
	}
}

// defaultSession applies the session defaults: admin 30d absolute with 24h
// idle, user 7d absolute with 24h idle.
func defaultSession(c *SessionConfig) {
	*c = SessionConfig{
		Admin: SessionAudienceConfig{
			CookieName:  "admin_session",
			MaxAge:      NewDuration(30 * 24 * time.Hour),
			IdleTimeout: NewDuration(24 * time.Hour),
		},
		User: SessionAudienceConfig{
			CookieName:  "user_session",
			MaxAge:      NewDuration(7 * 24 * time.Hour),
			IdleTimeout: NewDuration(24 * time.Hour),
		},
		ExtendOnActivity: NewBool(true),
		Secure:           "auto",
		HTTPOnly:         NewBool(true),
		SameSite:         "strict",
	}
}

// defaultRateLimit applies the per-IP sliding-window defaults: 120 reads
// and 10 writes per minute, 5 logins per 15 minutes.
func defaultRateLimit(c *RateLimitConfig) {
	*c = RateLimitConfig{
		Enabled:     NewBool(true),
		Read:        RateLimitRule{Requests: 120, Window: 60},
		Write:       RateLimitRule{Requests: 10, Window: 60},
		Health:      RateLimitRule{Requests: 120, Window: 60},
		GlobalBurst: 240,
		Auth: RateLimitAuthConfig{
			Login:         RateLimitRule{Requests: 5, Window: 900},
			PasswordReset: RateLimitRule{Requests: 3, Window: 3600},
			Registration:  RateLimitRule{Requests: 5, Window: 3600},
		},
	}
}

// defaultSecurity applies the encryption-key version and breach detection
// thresholds. The key itself is generated on first run by Validate.
func defaultSecurity(c *SecurityConfig) {
	*c = SecurityConfig{
		EncryptionKeyVersion: 1,
		BreachDetection: BreachDetectionConfig{
			BruteForce: BruteForceConfig{
				Attempts:      10,
				Window:        NewDuration(5 * time.Minute),
				BlockDuration: NewDuration(time.Hour),
			},
			CredentialStuffing: CredentialStuffingConfig{
				Attempts: 50,
				Window:   NewDuration(10 * time.Minute),
			},
			UnusualAccess: UnusualAccessConfig{NewCountryAlert: NewBool(true)},
		},
	}
}

// defaultContact applies the contact role defaults. security@ follows RFC
// 2142; abuse@ is deliberately empty because the server never advertises an
// address the operator has not provisioned.
func defaultContact(c *ContactConfig) {
	*c = ContactConfig{
		Admin:    ContactRole{Email: "admin@{fqdn}", Webhooks: defaultWebhooks()},
		Security: ContactRole{Email: "security@{fqdn}", Webhooks: defaultWebhooks()},
		Abuse:    ContactRole{Email: "", Webhooks: defaultWebhooks()},
		General:  ContactRole{Email: "", Webhooks: defaultWebhooks()},
	}
}

// defaultWebhooks returns the built-in transport slots. The map is open —
// any additional key is treated as a transport name at dispatch time.
func defaultWebhooks() map[string]string {
	return map[string]string{
		"telegram": "",
		"discord":  "",
		"slack":    "",
		"generic":  "",
	}
}

// defaultPrivacy applies the privacy and consent defaults. The consent
// model is opt-out: non-essential cookies stay on until a visitor declines.
func defaultPrivacy(c *PrivacyConfig) {
	*c = PrivacyConfig{
		Data: PrivacyDataConfig{
			Sold:           NewBool(false),
			StoredOnServer: NewBool(true),
			Sharing: []PrivacySharingEntry{
				{
					Condition: "analytics",
					When:      "Tracking configured (server.tracking.type set) AND user consents",
					Data:      "Anonymized: page views, browser type, country",
				},
				{
					Condition: "email",
					When:      "SMTP configured for sending emails",
					Data:      "Email address, message content",
				},
				{
					Condition: "user_initiated",
					When:      "User explicitly shares content (social buttons, exports)",
					Data:      "Whatever user chooses to share",
				},
			},
		},
		Retention: PrivacyRetentionConfig{
			Period:            "Account data is retained while your account is active. Upon account deletion, all personal data is permanently deleted within 30 days. Anonymized analytics data may be retained for up to 12 months.",
			ExportAvailable:   NewBool(true),
			DeletionAvailable: NewBool(true),
		},
		Consent: ConsentConfig{
			ShowUntilAcknowledged: NewBool(true),
			DefaultEnabled:        NewBool(true),
			Message:               "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data is stored on our servers and is never sold.",
			MessageIfSold:         "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data may be shared with or sold to third parties as described in our Privacy Policy.",
			Policy:                ConsentPolicyConfig{Text: "Privacy Policy", URL: "/server/privacy"},
			Buttons:               ConsentButtonsConfig{Decline: "Decline", Accept: "I Agree"},
			Position:              "bottom",
			ShowPreferences:       NewBool(true),
			PreferencesText:       "Manage Preferences",
			CookieName:            DefaultConsentCookieName,
		},
		Cookies: CookieCategoriesConfig{
			Essential: CookieCategory{
				Enabled:     NewBool(true),
				Description: "Required for the site to function. Includes session management, security tokens (CSRF), and authentication. These cookies are strictly necessary and cannot be disabled.",
			},
			Preferences: CookieCategory{
				Enabled:     NewBool(true),
				Description: "Remember your settings such as theme (dark/light), language, and UI preferences. Disabling will reset to defaults on each visit.",
			},
			Analytics: AnalyticsCookieCategory{
				Enabled:                  NewBool(true),
				Description:              "Help us understand how visitors use our site to improve the experience.",
				DescriptionSuffixNotSold: "Analytics data is anonymized and never sold.",
				DescriptionSuffixSold:    "Analytics data may be shared with third parties.",
			},
		},
	}
}

// defaultCache applies the cache defaults. memory works standalone; cluster
// and mixed mode require valkey or redis.
func defaultCache(c *CacheConfig) {
	*c = CacheConfig{
		Type:          "memory",
		Host:          "localhost",
		Port:          6379,
		DB:            0,
		TLS:           NewBool(false),
		TLSSkipVerify: NewBool(false),
		PoolSize:      10,
		MinIdle:       2,
		Timeout:       NewDuration(5 * time.Second),
		Prefix:        InternalName + ":",
		TTL:           NewDuration(time.Hour),
		Cluster:       NewBool(false),
	}
}

// defaultLogs applies the log level and per-log rotation policies.
func defaultLogs(c *LogsConfig) {
	*c = LogsConfig{
		Level: "warn",
		Dir:   LogDir(),
		Access: AccessLogFile{
			LogFile: LogFile{
				Enabled:  NewBool(true),
				Filename: "access.log",
				Format:   "apache",
				Rotate:   "monthly",
				Keep:     "none",
				Compress: NewBool(false),
			},
			LogHealthChecks: NewBool(false),
		},
		Server: LogFile{
			Enabled:  NewBool(true),
			Filename: "server.log",
			Format:   "text",
			Rotate:   "weekly,50MB",
			Keep:     "none",
			Compress: NewBool(false),
		},
		Error: LogFile{
			Enabled:  NewBool(true),
			Filename: "error.log",
			Format:   "text",
			Rotate:   "weekly,50MB",
			Keep:     "none",
			Compress: NewBool(false),
		},
		Security: LogFile{
			Enabled:  NewBool(true),
			Filename: "security.log",
			Format:   "json",
			Rotate:   "monthly",
			Keep:     "none",
			Compress: NewBool(false),
		},
		Scheduler: LogFile{
			Enabled:  NewBool(true),
			Filename: "scheduler.log",
			Format:   "text",
			Rotate:   "weekly",
			Keep:     "none",
			Compress: NewBool(false),
		},
		Audit: AuditLogFile{
			LogFile: LogFile{
				Enabled:  NewBool(true),
				Filename: "audit.log",
				Format:   "json",
				Rotate:   "daily",
				Keep:     "none",
				Compress: NewBool(false),
			},
			Events: AuditEvents{
				Authentication: NewBool(true),
				Configuration:  NewBool(true),
				Security:       NewBool(true),
				Tokens:         NewBool(true),
				DataAccess:     NewBool(false),
			},
		},
	}
}

// defaultScheduler applies the built-in task schedule. Every built-in task
// is enabled by default with the cadence from AI.md PART 5.
func defaultScheduler(c *SchedulerConfig) {
	*c = SchedulerConfig{
		Enabled: NewBool(true),
		Tasks: map[string]ScheduledTask{
			"geoip_update": {
				Enabled:     NewBool(true),
				Schedule:    "0 3 * * 0",
				RetryOnFail: NewBool(true),
				RetryDelay:  NewDuration(time.Hour),
			},
			"blocklist_update": {
				Enabled:     NewBool(true),
				Schedule:    "0 4 * * *",
				RetryOnFail: NewBool(true),
				RetryDelay:  NewDuration(time.Hour),
			},
			"cve_update": {
				Enabled:     NewBool(true),
				Schedule:    "0 5 * * *",
				RetryOnFail: NewBool(true),
				RetryDelay:  NewDuration(time.Hour),
			},
			"log_rotation": {
				Enabled:  NewBool(true),
				Schedule: "0 0 * * *",
			},
			"session_cleanup": {
				Enabled:  NewBool(true),
				Schedule: "@hourly",
			},
			"backup": {
				Enabled:   NewBool(true),
				Schedule:  "0 2 * * *",
				Retention: 4,
			},
			"ssl_renewal": {
				Enabled:     NewBool(true),
				Schedule:    "0 3 * * *",
				RenewBefore: NewDuration(7 * 24 * time.Hour),
			},
			"health_check": {
				Enabled:  NewBool(true),
				Schedule: "*/5 * * * *",
			},
			"tor_health": {
				Enabled:  NewBool(true),
				Schedule: "*/10 * * * *",
			},
		},
	}
}

// defaultBackup applies the backup destination and retention defaults: one
// full backup kept, capped at 10% of the backup volume.
func defaultBackup(c *BackupConfig) {
	*c = BackupConfig{
		Enabled: NewBool(true),
		Dir:     BackupDir(),
		Retention: BackupRetentionConfig{
			MaxBackups:   1,
			MaxTotalSize: "10%",
		},
		Encryption: BackupEncryptionConfig{Enabled: NewBool(false)},
	}
}

// defaultMetrics applies the metrics endpoint defaults. Every service token
// is empty, which means every metrics service answers 403 until configured.
func defaultMetrics(c *MetricsConfig) {
	*c = MetricsConfig{
		Enabled: NewBool(true),
		Root:    RootAliasConfig{Enabled: NewBool(true)},
		Auth: MetricsAuthConfig{
			AllowUnauthenticated: NewBool(false),
			Tokens: map[string]string{
				"prometheus": "",
				"grafana":    "",
				"loki":       "",
			},
		},
		IncludeSystem:   NewBool(true),
		IncludeRuntime:  NewBool(true),
		Loki:            MetricsLokiConfig{MaxEntries: 1000, MaxAge: NewDuration(time.Hour)},
		DurationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		SizeBuckets:     []float64{100, 1000, 10000, 100000, 1000000, 10000000},
	}
}

// defaultGeoIP applies the GeoIP defaults. Both country lists start empty,
// which means no country blocking is in effect.
func defaultGeoIP(c *GeoIPConfig) {
	*c = GeoIPConfig{
		Enabled: NewBool(true),
		Dir:     filepath.Join(DataDir(), "security", "geoip"),
		Databases: GeoIPDatabasesConfig{
			ASN:     NewBool(true),
			Country: NewBool(true),
			City:    NewBool(false),
		},
	}
}

// defaultNotifications applies the SMTP defaults. An empty host means the
// server autodetects a local MTA on startup.
func defaultNotifications(c *NotificationsConfig) {
	*c = NotificationsConfig{
		Email: EmailConfig{
			Enabled: NewBool(true),
			SMTP:    SMTPConfig{Port: 587, TLS: "auto"},
		},
	}
}

// defaultMaintenance applies the self-healing and cleanup defaults.
func defaultMaintenance(c *MaintenanceConfig) {
	*c = MaintenanceConfig{
		SelfHealing: SelfHealingConfig{
			Enabled:       NewBool(true),
			RetryInterval: NewDuration(30 * time.Second),
			MaxAttempts:   0,
		},
		Cleanup: MaintenanceCleanupConfig{
			DiskThreshold:    90,
			LogRetentionDays: 7,
			BackupKeepCount:  5,
		},
		Notify: MaintenanceNotifyConfig{
			OnEnter: NewBool(true),
			OnExit:  NewBool(true),
		},
	}
}

// defaultFeatures applies the project feature toggles. cashp ships with
// multi-user, organizations, and custom domains turned on.
func defaultFeatures(c *FeaturesConfig) {
	*c = FeaturesConfig{
		MultiUser:     NewBool(true),
		Organizations: NewBool(true),
		CustomDomains: CustomDomainsConfig{
			Enabled:           NewBool(true),
			MaxDomainsPerUser: 5,
			MaxDomainsPerOrg:  20,
			RequireSSL:        NewBool(true),
			AllowApex:         NewBool(true),
		},
		Tor:          NewBool(false),
		I2P:          NewBool(false),
		Registration: NewBool(true),
		APITokens:    NewBool(true),
	}
}

// defaultUsers applies the end-user account defaults. Registration is open
// with email verification required.
func defaultUsers(c *UsersConfig) {
	*c = UsersConfig{
		Enabled: NewBool(true),
		Registration: RegistrationConfig{
			Mode:                     "open",
			RequireEmailVerification: NewBool(true),
			InviteExpirationDays:     7,
		},
		Roles: RolesConfig{
			Available: []string{"admin", "user"},
			Default:   "user",
		},
		Tokens: UserTokensConfig{
			Enabled:    NewBool(true),
			MaxPerUser: 10,
		},
	}
}

// defaultOrgs applies the organization defaults: anyone authenticated can
// create one, and new organizations are publicly visible.
func defaultOrgs(c *OrgsConfig) {
	*c = OrgsConfig{
		Enabled:  NewBool(true),
		Creation: OrgCreationConfig{Mode: "open"},
		Profile:  OrgProfileConfig{DefaultVisibility: "public"},
		Members: OrgMembersConfig{
			DefaultRole:  "member",
			Require2FA:   NewBool(false),
			AllowInvites: NewBool(true),
		},
	}
}

// defaultI2P applies the eepsite defaults. I2P is opt-in: nothing is
// contacted and no port allocated while enabled is false.
func defaultI2P(c *I2PConfig) {
	*c = I2PConfig{
		Enabled:          NewBool(false),
		SAMAddress:       "127.0.0.1:7656",
		VirtualPort:      80,
		InboundLength:    3,
		OutboundLength:   3,
		InboundQuantity:  5,
		OutboundQuantity: 5,
		SignatureType:    7,
		BootstrapTimeout: NewDuration(5 * time.Minute),
	}
}
