package admin

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/web"
)

// fieldKind selects the control a setting is rendered with and the validation
// its submitted value must pass.
type fieldKind string

const (
	kindText     fieldKind = "text"
	kindNumber   fieldKind = "number"
	kindToggle   fieldKind = "toggle"
	kindSelect   fieldKind = "select"
	kindPassword fieldKind = "password"
	kindTextarea fieldKind = "textarea"
	kindTags     fieldKind = "tags"
	kindColor    fieldKind = "color"
	kindDuration fieldKind = "duration"
	kindEmail    fieldKind = "email"
	kindReadonly fieldKind = "readonly"
)

// option is one entry of a select control.
type option struct {
	Value string
	Label string
}

// field describes a single configurable setting.
type field struct {
	Key     string
	Label   string
	Kind    fieldKind
	Default string
	Help    string
	// Restart marks a setting that only takes effect after a restart, which the
	// form renders as a warning next to the control.
	Restart bool
	Options []option
	Min     int
	Max     int
}

// section groups related fields under a heading.
type section struct {
	Title  string
	Help   string
	Fields []field
}

// pageAction is a button the settings form offers besides Save. Every action is
// a POST, and a destructive one carries a confirmation prompt.
type pageAction struct {
	Name    string
	Label   string
	Confirm string
	Danger  bool
}

// settingsPage is one rendered configuration screen.
type settingsPage struct {
	Slug        string
	Title       string
	Description string
	Sections    []section
	Actions     []pageAction
}

// booleanOptions and other shared option sets keep the definitions below short.
var (
	modeOptions = []option{
		{Value: "production", Label: "Production"},
		{Value: "development", Label: "Development"},
		{Value: "debug", Label: "Debug"},
	}
	themeOptions = []option{
		{Value: "auto", Label: "Follow the device"},
		{Value: "dark", Label: "Dark"},
		{Value: "light", Label: "Light"},
	}
	tlsVersionOptions = []option{
		{Value: "1.2", Label: "TLS 1.2"},
		{Value: "1.3", Label: "TLS 1.3"},
	}
	challengeOptions = []option{
		{Value: "http-01", Label: "HTTP-01"},
		{Value: "dns-01", Label: "DNS-01"},
		{Value: "tls-alpn-01", Label: "TLS-ALPN-01"},
	}
	smtpTLSOptions = []option{
		{Value: "auto", Label: "Automatic"},
		{Value: "starttls", Label: "STARTTLS"},
		{Value: "tls", Label: "Implicit TLS"},
		{Value: "none", Label: "None"},
	}
	blocklistFormatOptions = []option{
		{Value: "cidr", Label: "CIDR"},
		{Value: "p2p", Label: "P2P (PeerGuardian)"},
		{Value: "dat", Label: "DAT (eMule)"},
		{Value: "plain", Label: "Plain IP"},
	}
)

// settingsPagesCache holds the page definitions, which are constant for the
// lifetime of the process.
var settingsPagesCache []settingsPage

// settingsPages returns every field-driven configuration screen the panel
// serves, in sidebar order.
func settingsPages() []settingsPage {
	if settingsPagesCache != nil {
		return settingsPagesCache
	}
	settingsPagesCache = buildSettingsPages()
	return settingsPagesCache
}

// settingsPageBySlug looks a page up by its route slug.
func settingsPageBySlug(slug string) (settingsPage, bool) {
	for _, page := range settingsPages() {
		if page.Slug == slug {
			return page, true
		}
	}
	return settingsPage{}, false
}

// buildSettingsPages declares every configuration screen. The field keys are
// the exact configuration keys the rest of the server reads.
func buildSettingsPages() []settingsPage {
	return []settingsPage{
		{
			Slug:        "config/settings",
			Title:       "Server settings",
			Description: "Core listener, process and identity settings.",
			Sections: []section{
				{
					Title: "General",
					Fields: []field{
						{Key: "port", Label: "Listen port", Kind: kindNumber, Default: "64580", Restart: true, Min: 1, Max: 65535, Help: "Port the server listens on."},
						{Key: "mode", Label: "Mode", Kind: kindSelect, Default: "production", Restart: true, Options: modeOptions},
						{Key: "fqdn", Label: "Fully qualified domain name", Kind: kindText, Help: "Left empty, the domain is detected from incoming requests."},
						{Key: "address", Label: "Listen address", Kind: kindText, Default: "[::]", Restart: true},
					},
				},
				{
					Title: "Process",
					Fields: []field{
						{Key: "daemonize", Label: "Detach from the terminal on start", Kind: kindToggle, Default: "false", Restart: true},
						{Key: "pidfile", Label: "Write a PID file", Kind: kindToggle, Default: "true", Restart: true},
					},
				},
				{
					Title: "Timezone",
					Fields: []field{
						{Key: settingTimezone, Label: "Server timezone", Kind: kindText, Default: "UTC", Help: "An IANA timezone name, such as America/New_York."},
					},
				},
			},
		},
		{
			Slug:        "config/branding",
			Title:       "Branding",
			Description: "How the public site presents itself.",
			Sections: []section{
				{
					Title: "Identity",
					Fields: []field{
						{Key: "title", Label: "Application title", Kind: kindText},
						{Key: "tagline", Label: "Tagline", Kind: kindText},
						{Key: "description", Label: "Description", Kind: kindTextarea, Help: "Used for the about page and search result snippets."},
						{Key: "logo", Label: "Logo URL", Kind: kindText, Help: "A path under /static or an absolute URL."},
						{Key: "favicon", Label: "Favicon URL", Kind: kindText},
						{Key: "theme", Label: "Default theme", Kind: kindSelect, Default: "auto", Options: themeOptions},
						{Key: "accent_color", Label: "Accent colour", Kind: kindColor, Default: "#007bff"},
					},
				},
				{
					Title: "Search engine metadata",
					Fields: []field{
						{Key: "keywords", Label: "Keywords", Kind: kindTags},
						{Key: "author", Label: "Author", Kind: kindText},
						{Key: "og_image", Label: "Social preview image URL", Kind: kindText},
						{Key: "twitter_handle", Label: "Twitter handle", Kind: kindText},
					},
				},
			},
		},
		{
			Slug:        "config/ssl",
			Title:       "TLS",
			Description: "Certificates and transport security.",
			Sections: []section{
				{
					Title: "Certificates",
					Fields: []field{
						{Key: "ssl.enabled", Label: "Serve HTTPS", Kind: kindToggle, Default: "false", Restart: true},
						{Key: "ssl.cert", Label: "Certificate path", Kind: kindText, Restart: true},
						{Key: "ssl.key", Label: "Private key path", Kind: kindText, Restart: true},
						{Key: "ssl.min_version", Label: "Minimum TLS version", Kind: kindSelect, Default: "1.2", Restart: true, Options: tlsVersionOptions},
					},
				},
				{
					Title: "Let's Encrypt",
					Fields: []field{
						{Key: "ssl.letsencrypt.enabled", Label: "Request certificates automatically", Kind: kindToggle, Default: "false"},
						{Key: "ssl.letsencrypt.email", Label: "Contact email", Kind: kindEmail},
						{Key: "ssl.letsencrypt.staging", Label: "Use the staging directory", Kind: kindToggle, Default: "false"},
						{Key: "ssl.letsencrypt.challenge", Label: "Challenge type", Kind: kindSelect, Default: "http-01", Options: challengeOptions},
					},
				},
			},
		},
		{
			Slug:        "config/email",
			Title:       "Email",
			Description: "Outbound mail transport.",
			Sections: []section{
				{
					Title: "SMTP",
					Fields: []field{
						{Key: "smtp.host", Label: "Host", Kind: kindText},
						{Key: "smtp.port", Label: "Port", Kind: kindNumber, Default: "587", Min: 1, Max: 65535},
						{Key: "smtp.username", Label: "Username", Kind: kindText},
						{Key: "smtp.password", Label: "Password", Kind: kindPassword, Help: "Stored encrypted. Leave blank to keep the current value."},
						{Key: "smtp.tls", Label: "Transport security", Kind: kindSelect, Default: "auto", Options: smtpTLSOptions},
					},
				},
				{
					Title: "Sender",
					Fields: []field{
						{Key: "from.name", Label: "Sender name", Kind: kindText},
						{Key: "from.email", Label: "Sender address", Kind: kindEmail},
					},
				},
			},
		},
		{
			Slug:        "config/notifications",
			Title:       "Server notifications",
			Description: "Which server events raise a notification.",
			Sections: []section{
				{
					Title: "Events",
					Fields: []field{
						{Key: "notifications.backup_success", Label: "Backup succeeded", Kind: kindToggle, Default: "false"},
						{Key: "notifications.backup_failure", Label: "Backup failed", Kind: kindToggle, Default: "true"},
						{Key: "notifications.ssl_expiring", Label: "Certificate expiring", Kind: kindToggle, Default: "true"},
						{Key: "notifications.ssl_expiring_days", Label: "Days of warning", Kind: kindNumber, Default: "14", Min: 1, Max: 90},
						{Key: "notifications.ssl_renewal_failure", Label: "Certificate renewal failed", Kind: kindToggle, Default: "true"},
						{Key: "notifications.security_alerts", Label: "Security alerts", Kind: kindToggle, Default: "true"},
						{Key: "notifications.update_available", Label: "Update available", Kind: kindToggle, Default: "true"},
					},
				},
			},
		},
		{
			Slug:        "config/scheduler",
			Title:       "Scheduler",
			Description: "Recurring maintenance tasks.",
			Sections: []section{
				{
					Title: "Scheduler",
					Fields: []field{
						{Key: "scheduler.enabled", Label: "Run scheduled tasks", Kind: kindToggle, Default: "true"},
					},
				},
				{
					Title: "Schedules",
					Help:  "Standard five-field cron expressions.",
					Fields: []field{
						{Key: "scheduler.backup_daily", Label: "Daily backup", Kind: kindText, Default: "0 2 * * *"},
						{Key: "scheduler.backup_hourly", Label: "Hourly incremental backup", Kind: kindText, Default: "0 * * * *"},
						{Key: "scheduler.session_purge", Label: "Expired session purge", Kind: kindText, Default: "*/15 * * * *"},
						{Key: "scheduler.geoip_update", Label: "GeoIP database update", Kind: kindText, Default: "0 3 * * *"},
						{Key: "scheduler.blocklist_update", Label: "Blocklist update", Kind: kindText, Default: "30 3 * * *"},
					},
				},
			},
		},
		{
			Slug:        "config/backup",
			Title:       "Backup",
			Description: "Scheduled backups, retention and encryption.",
			Sections: []section{
				{
					Title: "Schedule",
					Fields: []field{
						{Key: "backup.enabled", Label: "Daily backups", Kind: kindToggle, Default: "true"},
						{Key: "backup.hourly_enabled", Label: "Hourly incremental backups", Kind: kindToggle, Default: "false"},
						{Key: "backup.schedule", Label: "Daily schedule", Kind: kindText, Default: "0 2 * * *"},
					},
				},
				{
					Title: "Retention",
					Fields: []field{
						{Key: "backup.retention.max_backups", Label: "Daily backups kept", Kind: kindNumber, Default: "1", Min: 1, Max: 3650},
						{Key: "backup.retention.keep_weekly", Label: "Weekly backups kept", Kind: kindNumber, Default: "0", Min: 0, Max: 520},
						{Key: "backup.retention.keep_monthly", Label: "Monthly backups kept", Kind: kindNumber, Default: "0", Min: 0, Max: 120},
						{Key: "backup.retention.keep_yearly", Label: "Yearly backups kept", Kind: kindNumber, Default: "0", Min: 0, Max: 50},
						{Key: "backup.retention.max_total_size", Label: "Total size cap", Kind: kindText, Default: "10%", Help: "A percentage of the filesystem or an absolute size such as 50G. Use 0 to disable."},
					},
				},
				{
					Title: "Encryption",
					Fields: []field{
						{Key: "backup.encryption.enabled", Label: "Encrypt backup archives", Kind: kindToggle, Default: "false"},
						{Key: settingBackupKeyName, Label: "Encryption passphrase", Kind: kindPassword, Help: "Stored encrypted. Without it an encrypted backup cannot be restored."},
					},
				},
			},
		},
		{
			Slug:        "config/updates",
			Title:       "Updates",
			Description: "How new releases are discovered and applied.",
			Sections: []section{
				{
					Title: "Update channel",
					Fields: []field{
						{Key: "updates.check_enabled", Label: "Check for updates", Kind: kindToggle, Default: "true"},
						{Key: "updates.channel", Label: "Channel", Kind: kindSelect, Default: "stable", Options: []option{
							{Value: "stable", Label: "Stable"},
							{Value: "beta", Label: "Beta"},
						}},
						{Key: "updates.check_schedule", Label: "Check schedule", Kind: kindText, Default: "0 4 * * *"},
						{Key: "updates.auto_apply", Label: "Apply updates automatically", Kind: kindToggle, Default: "false", Help: "Applies only patch releases; a restart is still required."},
					},
				},
			},
		},
		{
			Slug:        "config/maintenance",
			Title:       "Maintenance",
			Description: "Take the public site offline and clear server state.",
			Sections: []section{
				{
					Title: "Maintenance mode",
					Fields: []field{
						{Key: "maintenance.enabled", Label: "Show the maintenance page", Kind: kindToggle, Default: "false"},
						{Key: "maintenance.message", Label: "Message", Kind: kindTextarea, Default: "We are performing scheduled maintenance and will be back shortly."},
						{Key: "maintenance.allowlist", Label: "Addresses that bypass maintenance", Kind: kindTags},
					},
				},
			},
			Actions: []pageAction{
				{Name: "purge_sessions", Label: "Purge expired sessions"},
				{Name: "revoke_sessions", Label: "Sign out every administrator", Danger: true,
					Confirm: "Every administrator, including you, will be signed out immediately."},
			},
		},
		{
			Slug:        "config/security/auth",
			Title:       "Authentication",
			Description: "Session, password and second-factor policy.",
			Sections: []section{
				{
					Title: "Sessions",
					Fields: []field{
						{Key: "session.timeout", Label: "Session lifetime", Kind: kindDuration, Default: "24h"},
						{Key: "session.extend_on_activity", Label: "Extend the session on activity", Kind: kindToggle, Default: "true"},
					},
				},
				{
					Title: "Second factor",
					Fields: []field{
						{Key: "mfa.enabled", Label: "Require a second factor for administrators", Kind: kindToggle, Default: "false"},
						{Key: "mfa.totp", Label: "Allow authenticator apps (TOTP)", Kind: kindToggle, Default: "true"},
						{Key: "mfa.recovery_codes", Label: "Allow recovery codes", Kind: kindToggle, Default: "true"},
					},
				},
				{
					Title: "Passwords",
					Fields: []field{
						{Key: "password.min_length", Label: "Minimum length", Kind: kindNumber, Default: "8", Min: 8, Max: 128},
						{Key: "password.require_uppercase", Label: "Require an upper case letter", Kind: kindToggle, Default: "true"},
						{Key: "password.require_number", Label: "Require a digit", Kind: kindToggle, Default: "true"},
						{Key: "password.require_special", Label: "Require a symbol", Kind: kindToggle, Default: "false"},
					},
				},
				{
					Title: "Account lockout",
					Fields: []field{
						{Key: "soft_lock_attempts", Label: "Attempts before a soft lock", Kind: kindNumber, Default: "5", Min: 1, Max: 100},
						{Key: "soft_lock_duration", Label: "Soft lock duration", Kind: kindDuration, Default: "15m"},
						{Key: "hard_lock_attempts", Label: "Attempts before a hard lock", Kind: kindNumber, Default: "10", Min: 1, Max: 200},
						{Key: "hard_lock_duration", Label: "Hard lock duration", Kind: kindDuration, Default: "1h"},
						{Key: "permanent_lock_attempts", Label: "Attempts before a permanent lock", Kind: kindNumber, Default: "15", Min: 1, Max: 500},
					},
				},
				{
					Title: "External identity providers",
					Help:  "Providers are configured in the server configuration file; this switch controls whether the sign-in page offers them.",
					Fields: []field{
						{Key: "auth.oidc.enabled", Label: "Offer OIDC sign-in", Kind: kindToggle, Default: "false"},
						{Key: "auth.ldap.enabled", Label: "Offer LDAP sign-in", Kind: kindToggle, Default: "false"},
						{Key: "auth.saml.enabled", Label: "Offer SAML sign-in", Kind: kindToggle, Default: "false"},
					},
				},
			},
		},
		{
			Slug:        "config/security/ratelimit",
			Title:       "Rate limits",
			Description: "Per-address request ceilings.",
			Sections: []section{
				{
					Title: "Limits",
					Fields: []field{
						{Key: "rate_limit.enabled", Label: "Enforce rate limits", Kind: kindToggle, Default: "true"},
						{Key: "rate_limit.read.requests", Label: "Read requests per window", Kind: kindNumber, Default: "120", Min: 1, Max: 100000},
						{Key: "rate_limit.read.window", Label: "Read window", Kind: kindDuration, Default: "1m"},
						{Key: "rate_limit.write.requests", Label: "Write requests per window", Kind: kindNumber, Default: "10", Min: 1, Max: 100000},
						{Key: "rate_limit.write.window", Label: "Write window", Kind: kindDuration, Default: "1m"},
						{Key: "rate_limit.health.requests", Label: "Health requests per window", Kind: kindNumber, Default: "120", Min: 1, Max: 100000},
						{Key: "rate_limit.health.window", Label: "Health window", Kind: kindDuration, Default: "1m"},
						{Key: "rate_limit.global_burst", Label: "Absolute per-address ceiling", Kind: kindNumber, Default: "240", Min: 1, Max: 1000000},
					},
				},
			},
		},
		{
			Slug:        "config/security/firewall",
			Title:       "Firewall",
			Description: "Blocking policy for abusive addresses, and the browser security headers.",
			Sections: []section{
				{
					Title: "Address blocking",
					Fields: []field{
						{Key: "ip_block.enabled", Label: "Block abusive addresses", Kind: kindToggle, Default: "true"},
						{Key: "ip_block.escalation", Label: "Escalate repeat offenders", Kind: kindToggle, Default: "true"},
						{Key: "ip_block.first_duration", Label: "First block duration", Kind: kindDuration, Default: "1h"},
						{Key: "ip_block.max_duration", Label: "Maximum block duration", Kind: kindDuration, Default: "168h"},
						{Key: "blocklist", Label: "Always blocked addresses", Kind: kindTags},
					},
				},
				{
					Title: "Browser security headers",
					Fields: []field{
						{Key: "csp.enabled", Label: "Content Security Policy", Kind: kindToggle, Default: "true"},
						{Key: "hsts.enabled", Label: "HTTP Strict Transport Security", Kind: kindToggle, Default: "true"},
						{Key: "hsts.max_age", Label: "HSTS max age", Kind: kindDuration, Default: "8760h"},
						{Key: "cors.enabled", Label: "Cross-origin resource sharing", Kind: kindToggle, Default: "true"},
						{Key: "cors.origins", Label: "Allowed origins", Kind: kindTags, Default: "*"},
						{Key: "cors.methods", Label: "Allowed methods", Kind: kindTags, Default: "GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS"},
					},
				},
			},
		},
		{
			Slug:        "config/security/allowlist",
			Title:       "Allowlist",
			Description: "Trusted addresses that bypass blocklists, rate limits and GeoIP rules. They never bypass authentication.",
			Sections: []section{
				{
					Title: "Trusted addresses",
					Fields: []field{
						{Key: "allowlist", Label: "Addresses and ranges", Kind: kindTags, Help: "Single addresses or CIDR ranges."},
						{Key: "allowlist.trust_proxy_headers", Label: "Trust forwarded headers from these addresses", Kind: kindToggle, Default: "false"},
					},
				},
			},
		},
		{
			Slug:        "config/network/tor",
			Title:       "Tor",
			Description: "Onion service publication.",
			Sections: []section{
				{
					Title: "Hidden service",
					Fields: []field{
						{Key: "tor.enabled", Label: "Publish an onion service", Kind: kindToggle, Default: "false"},
						{Key: "tor.control_address", Label: "Tor control address", Kind: kindText, Default: "127.0.0.1:9051"},
						{Key: "tor.virtual_port", Label: "Onion port", Kind: kindNumber, Default: "80", Min: 1, Max: 65535},
					},
				},
			},
		},
		{
			Slug:        "config/network/i2p",
			Title:       "I2P",
			Description: "I2P tunnel publication.",
			Sections: []section{
				{
					Title: "Tunnel",
					Fields: []field{
						{Key: "i2p.enabled", Label: "Publish an I2P tunnel", Kind: kindToggle, Default: "false"},
						{Key: "i2p.sam_address", Label: "SAM bridge address", Kind: kindText, Default: "127.0.0.1:7656"},
						{Key: "i2p.tunnel_name", Label: "Tunnel name", Kind: kindText},
					},
				},
			},
		},
		{
			Slug:        "config/network/geoip",
			Title:       "GeoIP",
			Description: "Country lookups and country-based access rules.",
			Sections: []section{
				{
					Title: "Database",
					Fields: []field{
						{Key: "geoip.enabled", Label: "Resolve addresses to countries", Kind: kindToggle, Default: "true"},
						{Key: "geoip.auto_update", Label: "Update the database automatically", Kind: kindToggle, Default: "true"},
						{Key: "geoip.update_schedule", Label: "Update schedule", Kind: kindText, Default: "0 3 * * *"},
					},
				},
				{
					Title: "Country rules",
					Help:  "ISO 3166-1 alpha-2 codes. An allow list, when set, overrides the deny list.",
					Fields: []field{
						{Key: "geoip.deny_countries", Label: "Denied countries", Kind: kindTags},
						{Key: "geoip.allow_countries", Label: "Allowed countries", Kind: kindTags},
					},
				},
			},
		},
		{
			Slug:        "config/network/blocklists",
			Title:       "Blocklists",
			Description: "External address blocklists downloaded on a schedule.",
			Sections: []section{
				{
					Title: "Downloads",
					Fields: []field{
						{Key: "blocklists.enabled", Label: "Enforce downloaded blocklists", Kind: kindToggle, Default: "true"},
						{Key: "blocklists.auto_update", Label: "Update automatically", Kind: kindToggle, Default: "true"},
						{Key: "blocklists.update_schedule", Label: "Update schedule", Kind: kindText, Default: "30 3 * * *"},
						{Key: "blocklists.default_format", Label: "Default format", Kind: kindSelect, Default: "cidr", Options: blocklistFormatOptions},
					},
				},
				{
					Title: "Sources",
					Help:  "One URL per line. Comment lines starting with # are ignored, and gzipped sources are decompressed on download.",
					Fields: []field{
						{Key: "blocklists.sources", Label: "Blocklist URLs", Kind: kindTextarea},
					},
				},
			},
		},
		{
			Slug:        "config/moderation/users",
			Title:       "User policy",
			Description: "Registration and moderation rules for application users.",
			Sections: []section{
				{
					Title: "Registration",
					Fields: []field{
						{Key: "users.registration_enabled", Label: "Allow self-registration", Kind: kindToggle, Default: "false"},
						{Key: settingMultiUser, Label: "Allow more than one user account", Kind: kindToggle, Default: "false"},
						{Key: "users.require_email_verification", Label: "Require email verification", Kind: kindToggle, Default: "true"},
						{Key: "users.blocked_email_domains", Label: "Blocked email domains", Kind: kindTags},
					},
				},
				{
					Title: "Moderation",
					Fields: []field{
						{Key: "users.auto_suspend_reports", Label: "Reports before automatic suspension", Kind: kindNumber, Default: "0", Min: 0, Max: 1000, Help: "Zero disables automatic suspension."},
						{Key: "users.suspension_duration", Label: "Default suspension duration", Kind: kindDuration, Default: "72h"},
					},
				},
			},
		},
		{
			Slug:        "config/users/invites",
			Title:       "Invite policy",
			Description: "How invitations to the application behave.",
			Sections: []section{
				{
					Title: "Invitations",
					Fields: []field{
						{Key: "invites.enabled", Label: "Allow invitations", Kind: kindToggle, Default: "true"},
						{Key: "invites.require_invite", Label: "Registration requires an invitation", Kind: kindToggle, Default: "true"},
						{Key: "invites.default_expiry", Label: "Default expiry", Kind: kindDuration, Default: "24h"},
						{Key: "invites.max_uses", Label: "Maximum uses per invitation", Kind: kindNumber, Default: "1", Min: 1, Max: 1000},
						{Key: "invites.per_user_limit", Label: "Open invitations per user", Kind: kindNumber, Default: "5", Min: 0, Max: 1000},
					},
				},
			},
		},
		{
			Slug:        "config/cluster/add",
			Title:       "Add a node",
			Description: "Defaults applied to nodes joining this cluster.",
			Sections: []section{
				{
					Title: "Join defaults",
					Fields: []field{
						{Key: "cluster.enabled", Label: "Accept managed nodes", Kind: kindToggle, Default: "false"},
						{Key: "cluster.join_window", Label: "Join token lifetime", Kind: kindDuration, Default: "1h"},
						{Key: "cluster.default_port", Label: "Agent port", Kind: kindNumber, Default: "64581", Min: 1, Max: 65535},
						{Key: "cluster.verify_tls", Label: "Verify the agent certificate", Kind: kindToggle, Default: "true"},
					},
				},
			},
		},
		{
			Slug:        "config/url",
			Title:       "URL detection",
			Description: "How the server learns the domains it is reached on.",
			Sections: []section{
				{
					Title: "Learning",
					Fields: []field{
						{Key: "url_detection.learning", Label: "Learn domain patterns", Kind: kindToggle, Default: "true"},
						{Key: "url_detection.min_samples", Label: "Samples before a wildcard is inferred", Kind: kindNumber, Default: "3", Min: 1, Max: 1000},
						{Key: "url_detection.sample_window", Label: "Sample window", Kind: kindDuration, Default: "5m"},
						{Key: "url_detection.log_changes", Label: "Log domain changes", Kind: kindToggle, Default: "true"},
						{Key: "url_detection.live_reload", Label: "Apply detected domains immediately", Kind: kindToggle, Default: "true"},
					},
				},
			},
		},
	}
}

// renderedField is one control prepared for the template.
type renderedField struct {
	field
	Value string
	// Stored reports, for a password field, whether a value is already saved.
	// The value itself is never sent to the browser.
	Stored bool
}

// renderedSection is a section with its values filled in.
type renderedSection struct {
	Title  string
	Help   string
	Fields []renderedField
}

// slugFor derives the settings slug from a request path.
func (p *Panel) slugFor(req *http.Request) string {
	return strings.TrimPrefix(strings.TrimPrefix(req.URL.Path, p.base()), "/")
}

// handleSettingsGet renders a configuration screen.
func (p *Panel) handleSettingsGet(w http.ResponseWriter, req *http.Request) {
	page, ok := settingsPageBySlug(p.slugFor(req))
	if !ok {
		p.handleNotFound(w, req)
		return
	}
	p.renderSettings(w, req, page, http.StatusOK)
}

// renderSettings loads the current values and renders the form.
func (p *Panel) renderSettings(w http.ResponseWriter, req *http.Request, page settingsPage, status int) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, page.Title, page.Description)
	ctx.PageClass = "panel panel-settings"

	sections := make([]renderedSection, 0, len(page.Sections))
	for _, sec := range page.Sections {
		rendered := renderedSection{Title: sec.Title, Help: sec.Help}
		for _, f := range sec.Fields {
			item := renderedField{field: f}
			if f.Kind == kindPassword {
				item.Stored = p.hasSecret(req.Context(), f.Key)
			} else {
				item.Value = p.settingValue(req.Context(), f)
			}
			rendered.Fields = append(rendered.Fields, item)
		}
		sections = append(sections, rendered)
	}

	ctx.Data = map[string]any{
		"Page":     page,
		"Sections": sections,
		"Actions":  page.Actions,
	}
	p.renderStatus(w, req, status, "settings", ctx)
}

// settingValue returns the stored value of a field, or its default.
func (p *Panel) settingValue(ctx context.Context, f field) string {
	value, ok, err := p.setting(ctx, f.Key)
	if err != nil || !ok {
		return f.Default
	}
	return value
}

// handleSettingsPost validates and stores a submitted configuration form.
func (p *Panel) handleSettingsPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	page, ok := settingsPageBySlug(p.slugFor(req))
	if !ok {
		p.handleNotFound(w, req)
		return
	}
	rec := adminFromContext(req.Context())

	if action := req.PostFormValue("action"); action != "" && action != "save" {
		p.runSettingsAction(w, req, page, rec, action)
		return
	}

	changed := make([]string, 0, 8)
	for _, sec := range page.Sections {
		for _, f := range sec.Fields {
			if f.Kind == kindReadonly {
				continue
			}
			if f.Kind == kindPassword {
				secret := req.PostFormValue(f.Key)
				if secret == "" {
					continue
				}
				if err := p.storeSecret(req.Context(), f.Key, secret); err != nil {
					p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
					return
				}
				changed = append(changed, f.Key)
				continue
			}

			value, err := normalizeFieldValue(f, req.PostForm.Has(f.Key), req.PostFormValue(f.Key))
			if err != nil {
				p.settingsError(w, req, page, f, err)
				return
			}
			current := p.settingValue(req.Context(), f)
			if value == current {
				continue
			}
			if err := p.putSetting(req.Context(), f.Key, value, rec.Username); err != nil {
				p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
				return
			}
			changed = append(changed, f.Key)
		}
	}

	if len(changed) == 0 {
		p.redirect(w, req, page.Slug, "info", "Nothing changed.")
		return
	}
	sort.Strings(changed)
	p.recordAudit(req.Context(), "settings", "settings_updated", rec.Username, page.Slug, "changed: "+strings.Join(changed, ", "))
	p.redirect(w, req, page.Slug, "success", fmt.Sprintf("Saved %d %s.", len(changed), pluralWord(len(changed), "setting", "settings")))
}

// settingsError re-renders the form with a validation message for one field.
func (p *Panel) settingsError(w http.ResponseWriter, req *http.Request, page settingsPage, f field, err error) {
	web.AddFlash(w, req, "error", f.Label+": "+err.Error())
	p.renderSettings(w, req, page, http.StatusBadRequest)
}

// runSettingsAction performs a named button action on a settings page.
func (p *Panel) runSettingsAction(w http.ResponseWriter, req *http.Request, page settingsPage, rec *adminRecord, action string) {
	allowed := false
	for _, candidate := range page.Actions {
		if candidate.Name == action {
			allowed = true
			break
		}
	}
	if !allowed {
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
		return
	}

	switch action {
	case "purge_sessions":
		if err := p.purgeExpiredSessions(req.Context()); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "maintenance", "sessions_purged", rec.Username, "", "expired admin sessions removed")
		p.redirect(w, req, page.Slug, "success", "Expired sessions were removed.")
	case "revoke_sessions":
		if req.PostFormValue("confirm") != "yes" {
			p.redirect(w, req, page.Slug, "error", "That action needs to be confirmed.")
			return
		}
		if err := p.revokeAllSessions(req.Context()); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "maintenance", "sessions_revoked", rec.Username, "", "every admin session revoked")
		p.clearSessionCookie(w, req)
		p.redirect(w, req, "", "info", "Every administrator was signed out.")
	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// pluralWord picks the singular or plural form for a count.
func pluralWord(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// colorPattern matches a six-digit hexadecimal colour.
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// tagPattern bounds a single tag value so a stored list stays printable.
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9._:/@*+-]{1,253}$`)

// normalizeFieldValue validates a submitted value and returns the form that is
// stored. present reports whether the form carried the key at all, which is how
// an unchecked toggle is recognised.
func normalizeFieldValue(f field, present bool, raw string) (string, error) {
	value := strings.TrimSpace(raw)

	switch f.Kind {
	case kindToggle:
		if present && value != "" && value != "false" && value != "off" {
			return "true", nil
		}
		return "false", nil

	case kindNumber:
		if value == "" {
			return f.Default, nil
		}
		number, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("enter a whole number")
		}
		if f.Max > 0 && (number < f.Min || number > f.Max) {
			return "", fmt.Errorf("enter a number between %d and %d", f.Min, f.Max)
		}
		return strconv.Itoa(number), nil

	case kindSelect:
		for _, opt := range f.Options {
			if opt.Value == value {
				return value, nil
			}
		}
		return "", fmt.Errorf("choose one of the listed values")

	case kindColor:
		if value == "" {
			return f.Default, nil
		}
		if !colorPattern.MatchString(value) {
			return "", fmt.Errorf("use a colour such as #007bff")
		}
		return strings.ToLower(value), nil

	case kindDuration:
		if value == "" {
			return f.Default, nil
		}
		parsed, err := parseDuration(value)
		if err != nil {
			return "", err
		}
		return parsed.String(), nil

	case kindEmail:
		if value == "" {
			return "", nil
		}
		if !validEmail(value) {
			return "", fmt.Errorf("enter a valid email address")
		}
		return value, nil

	case kindTags:
		return normalizeTags(value)

	case kindTextarea:
		if len(value) > 65536 {
			return "", fmt.Errorf("that text is too long")
		}
		return strings.ReplaceAll(value, "\r\n", "\n"), nil

	default:
		if len(value) > 1024 {
			return "", fmt.Errorf("that value is too long")
		}
		if strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("that value must be a single line")
		}
		return value, nil
	}
}

// normalizeTags turns a comma or newline separated list into a canonical
// comma-separated value with duplicates removed.
func normalizeTags(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})

	seen := make(map[string]bool, len(fields))
	tags := make([]string, 0, len(fields))
	for _, item := range fields {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		if item != "*" && !tagPattern.MatchString(item) {
			return "", fmt.Errorf("%q is not a valid entry", item)
		}
		seen[item] = true
		tags = append(tags, item)
	}
	return strings.Join(tags, ","), nil
}

// parseDuration accepts Go durations plus the day, week and year suffixes an
// operator expects in a retention or lockout setting.
func parseDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, fmt.Errorf("enter a duration such as 15m, 24h or 7d")
	}

	multipliers := map[string]time.Duration{
		"d": 24 * time.Hour,
		"w": 7 * 24 * time.Hour,
		"y": 365 * 24 * time.Hour,
	}
	for suffix, unit := range multipliers {
		if !strings.HasSuffix(trimmed, suffix) {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSuffix(trimmed, suffix))
		if err != nil || count < 0 {
			return 0, fmt.Errorf("enter a duration such as 15m, 24h or 7d")
		}
		return time.Duration(count) * unit, nil
	}

	parsed, err := time.ParseDuration(trimmed)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("enter a duration such as 15m, 24h or 7d")
	}
	return parsed, nil
}
