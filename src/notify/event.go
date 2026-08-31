package notify

import "sort"

// Event categories from the notification preference tables in AI.md PART 18
// -> "Admin Notification Preferences" and "User Notification Preferences".
const (
	// CategorySecurity groups the events an account holder may never turn
	// off: login alerts, credential changes and breach notices.
	CategorySecurity = "security"
	// CategoryServer groups certificate, update and capacity events.
	CategoryServer = "server"
	// CategoryBackup groups backup outcomes.
	CategoryBackup = "backup"
	// CategoryScheduler groups scheduled-task outcomes.
	CategoryScheduler = "scheduler"
	// CategoryAdmins groups other administrators' activity.
	CategoryAdmins = "admins"
	// CategoryAccount groups profile and verification changes.
	CategoryAccount = "account"
	// CategorySessions groups session and device events.
	CategorySessions = "sessions"
	// CategoryLifecycle groups startup and shutdown notices.
	CategoryLifecycle = "lifecycle"
)

// Event is one catalog entry: the identity of a notifiable occurrence plus
// the defaults that decide where it goes. The decision matrix in AI.md
// PART 18 -> "Notification vs Email Decision Matrix" is encoded here rather
// than being re-derived at each call site.
type Event struct {
	// Name is the stable identifier callers pass in Message.Event.
	Name string
	// Category is one of the Category* constants and drives the grouping in
	// the preference UI.
	Category string
	// Title is the default WebUI headline.
	Title string
	// Type is the default notification type.
	Type Type
	// Surfaces is the default WebUI placement set.
	Surfaces Surface
	// Template names the email template. An empty value means this event is
	// never emailed.
	Template string
	// Audience selects which preference store and notification table apply.
	Audience Audience
	// DefaultWebUI is the shipped default for the WebUI toggle.
	DefaultWebUI bool
	// DefaultEmail is the shipped default for the email toggle.
	DefaultEmail bool
	// Required marks a security event that cannot be disabled by anyone.
	Required bool
	// Webhook marks an event that also fans out to the configured contact
	// webhooks. Per-user account events never do: their content is personal
	// and PART 12 forbids putting user content on the admin webhooks.
	Webhook bool
	// SuppressedBy lists events that, when dispatched for the same
	// ExecutionID, cancel this one. PART 18 requires one notification per
	// failed run, not two.
	SuppressedBy []string
}

// Catalog event names. Callers reference these constants rather than raw
// strings so a typo is a compile error.
const (
	EventSettingsSaved      = "settings_saved"
	EventConfigError        = "config_error"
	EventBackupStarted      = "backup_started"
	EventBackupComplete     = "backup_complete"
	EventBackupFailed       = "backup_failed"
	EventSSLExpiring        = "ssl_expiring"
	EventSSLRenewed         = "ssl_renewed"
	EventSSLRenewalFailed   = "ssl_renewal_failed"
	EventUpdateAvailable    = "update_available"
	EventUpdateInstalled    = "update_installed"
	EventSchedulerError     = "scheduler_error"
	EventAdminLogin         = "admin_login"
	EventAdminLogout        = "admin_logout"
	EventSMTPNotConfigured  = "smtp_not_configured"
	EventDatabaseIssue      = "database_issue"
	EventDiskSpaceLow       = "disk_space_low"
	EventGeoIPOutdated      = "geoip_outdated"
	EventTorReady           = "tor_ready"
	EventStartup            = "startup"
	EventShutdown           = "shutdown"
	EventBreachAdminAlert   = "breach_admin_alert"
	EventBreachNotification = "breach_notification"
	EventWelcome            = "welcome"
	EventEmailVerify        = "email_verify"
	EventEmailVerified      = "email_verified"
	EventPasswordReset      = "password_reset"
	EventPasswordChanged    = "password_changed"
	EventLoginAlert         = "login_alert"
	EventSecurityAlert      = "security_alert"
	EventMFAReminder        = "mfa_reminder"
	EventTwoFactorEnabled   = "2fa_enabled"
	EventTwoFactorDisabled  = "2fa_disabled"
	EventTokenRegenerated   = "token_regenerated"
	EventTokenCreated       = "token_created"
	EventTokenRevoked       = "token_revoked"
	EventProfileUpdated     = "profile_updated"
	EventSessionExpired     = "session_expired"
	EventAccountSuspended   = "account_suspended"
	EventPasswordResetForce = "password_reset_required"
	EventRecoveryKeysLow    = "recovery_keys_low"
	EventRecoveryKeyUsed    = "recovery_key_used"
	EventTest               = "test"
)

// catalog holds every notifiable event. Registration order is irrelevant;
// Events() sorts by name so the admin panel and tests see a stable list.
var catalog = buildCatalog(
	// Server administration events. These fan out to the contact webhooks
	// because they describe the server, never a person.
	Event{Name: EventSettingsSaved, Category: CategoryServer, Title: "Settings saved", Type: TypeSuccess, Surfaces: SurfaceToast, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventConfigError, Category: CategoryServer, Title: "Configuration error", Type: TypeError, Surfaces: SurfaceToast, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventBackupStarted, Category: CategoryBackup, Title: "Backup started", Type: TypeInfo, Surfaces: SurfaceToast, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventBackupComplete, Category: CategoryBackup, Title: "Backup complete", Type: TypeSuccess, Surfaces: SurfaceToast | SurfaceCenter, Template: "backup_complete", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventBackupFailed, Category: CategoryBackup, Title: "Backup failed", Type: TypeError, Surfaces: SurfaceToast | SurfaceCenter, Template: "backup_failed", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true},
	Event{Name: EventSSLExpiring, Category: CategoryServer, Title: "SSL certificate expiring", Type: TypeWarning, Surfaces: SurfaceBanner | SurfaceCenter, Template: "ssl_expiring", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true},
	Event{Name: EventSSLRenewed, Category: CategoryServer, Title: "SSL certificate renewed", Type: TypeSuccess, Surfaces: SurfaceToast | SurfaceCenter, Template: "ssl_renewed", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventSSLRenewalFailed, Category: CategoryServer, Title: "SSL renewal failed", Type: TypeError, Surfaces: SurfaceToast | SurfaceBanner | SurfaceCenter, Template: "ssl_renewal_failed", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true},
	Event{Name: EventUpdateAvailable, Category: CategoryServer, Title: "Update available", Type: TypeInfo, Surfaces: SurfaceBanner | SurfaceCenter, Template: "update_available", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventUpdateInstalled, Category: CategoryServer, Title: "Update installed", Type: TypeSuccess, Surfaces: SurfaceCenter, Template: "update_installed", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true},
	Event{Name: EventSchedulerError, Category: CategoryScheduler, Title: "Scheduled task failed", Type: TypeError, Surfaces: SurfaceToast | SurfaceCenter, Template: "scheduler_error", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true, SuppressedBy: []string{EventBackupFailed, EventSSLRenewalFailed}},
	Event{Name: EventAdminLogin, Category: CategoryAdmins, Title: "Administrator logged in", Type: TypeInfo, Surfaces: SurfaceCenter, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventAdminLogout, Category: CategoryAdmins, Title: "Administrator logged out", Type: TypeInfo, Surfaces: SurfaceCenter, Audience: AudienceAdmin},
	Event{Name: EventSMTPNotConfigured, Category: CategoryServer, Title: "SMTP not configured", Type: TypeWarning, Surfaces: SurfaceBanner, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventDatabaseIssue, Category: CategoryServer, Title: "Database connection issue", Type: TypeError, Surfaces: SurfaceBanner | SurfaceCenter, Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventDiskSpaceLow, Category: CategoryServer, Title: "Disk space low", Type: TypeWarning, Surfaces: SurfaceBanner | SurfaceCenter, Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Webhook: true},
	Event{Name: EventGeoIPOutdated, Category: CategoryServer, Title: "GeoIP database outdated", Type: TypeInfo, Surfaces: SurfaceCenter, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventTorReady, Category: CategoryServer, Title: "Tor address ready", Type: TypeSuccess, Surfaces: SurfaceToast, Audience: AudienceAdmin, DefaultWebUI: true},
	Event{Name: EventStartup, Category: CategoryLifecycle, Title: "Server started", Type: TypeInfo, Surfaces: SurfaceCenter, Template: "startup", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventShutdown, Category: CategoryLifecycle, Title: "Server stopped", Type: TypeInfo, Surfaces: SurfaceCenter, Template: "shutdown", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},
	Event{Name: EventBreachAdminAlert, Category: CategorySecurity, Title: "Security breach detected", Type: TypeSecurity, Surfaces: SurfaceBanner | SurfaceCenter, Template: "breach_admin_alert", Audience: AudienceAdmin, DefaultWebUI: true, DefaultEmail: true, Required: true, Webhook: true},
	Event{Name: EventTest, Category: CategoryServer, Title: "Test notification", Type: TypeInfo, Surfaces: SurfaceToast, Template: "test", Audience: AudienceAdmin, DefaultWebUI: true, Webhook: true},

	// Account events. Every one of these carries personal content, so none
	// of them is ever pushed to a contact webhook.
	Event{Name: EventWelcome, Category: CategoryAccount, Title: "Welcome", Type: TypeSuccess, Surfaces: SurfaceCenter, Template: "welcome", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true},
	Event{Name: EventEmailVerify, Category: CategoryAccount, Title: "Verify your email", Type: TypeInfo, Template: "email_verify", Audience: AudienceBoth, DefaultEmail: true, Required: true},
	Event{Name: EventEmailVerified, Category: CategoryAccount, Title: "Email verified", Type: TypeSuccess, Surfaces: SurfaceToast | SurfaceCenter, Audience: AudienceUser, DefaultWebUI: true, DefaultEmail: true},
	Event{Name: EventPasswordReset, Category: CategorySecurity, Title: "Password reset requested", Type: TypeSecurity, Template: "password_reset", Audience: AudienceBoth, DefaultEmail: true, Required: true},
	Event{Name: EventPasswordChanged, Category: CategorySecurity, Title: "Password changed", Type: TypeSecurity, Surfaces: SurfaceToast | SurfaceCenter, Template: "password_changed", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventLoginAlert, Category: CategorySecurity, Title: "New login detected", Type: TypeSecurity, Surfaces: SurfaceCenter, Template: "login_alert", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventSecurityAlert, Category: CategorySecurity, Title: "Security alert", Type: TypeSecurity, Surfaces: SurfaceToast | SurfaceCenter, Template: "security_alert", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventMFAReminder, Category: CategorySecurity, Title: "Secure your account", Type: TypeWarning, Surfaces: SurfaceBanner, Template: "mfa_reminder", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true},
	Event{Name: EventTwoFactorEnabled, Category: CategorySecurity, Title: "Two-factor authentication enabled", Type: TypeSecurity, Surfaces: SurfaceToast | SurfaceCenter, Template: "2fa_enabled", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventTwoFactorDisabled, Category: CategorySecurity, Title: "Two-factor authentication disabled", Type: TypeSecurity, Surfaces: SurfaceToast | SurfaceCenter, Template: "2fa_disabled", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventTokenRegenerated, Category: CategorySecurity, Title: "API token regenerated", Type: TypeSecurity, Surfaces: SurfaceToast | SurfaceCenter, Template: "token_regenerated", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventTokenCreated, Category: CategoryAccount, Title: "API token created", Type: TypeSuccess, Surfaces: SurfaceToast | SurfaceCenter, Audience: AudienceBoth, DefaultWebUI: true},
	Event{Name: EventTokenRevoked, Category: CategoryAccount, Title: "API token revoked", Type: TypeWarning, Surfaces: SurfaceToast | SurfaceCenter, Audience: AudienceBoth, DefaultWebUI: true},
	Event{Name: EventProfileUpdated, Category: CategoryAccount, Title: "Profile updated", Type: TypeSuccess, Surfaces: SurfaceToast, Audience: AudienceUser, DefaultWebUI: true},
	Event{Name: EventSessionExpired, Category: CategorySessions, Title: "Session expired", Type: TypeInfo, Surfaces: SurfaceToast, Audience: AudienceUser, DefaultWebUI: true},
	Event{Name: EventAccountSuspended, Category: CategoryAccount, Title: "Account suspended", Type: TypeError, Surfaces: SurfaceBanner, Audience: AudienceUser, DefaultWebUI: true},
	Event{Name: EventPasswordResetForce, Category: CategorySecurity, Title: "Password reset required", Type: TypeWarning, Surfaces: SurfaceBanner, Audience: AudienceUser, DefaultWebUI: true, Required: true},
	Event{Name: EventRecoveryKeysLow, Category: CategorySecurity, Title: "Recovery keys running low", Type: TypeWarning, Surfaces: SurfaceCenter, Audience: AudienceBoth, DefaultWebUI: true},
	Event{Name: EventRecoveryKeyUsed, Category: CategorySecurity, Title: "Recovery key used", Type: TypeSecurity, Surfaces: SurfaceCenter, Template: "security_alert", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
	Event{Name: EventBreachNotification, Category: CategorySecurity, Title: "Important security notice", Type: TypeSecurity, Surfaces: SurfaceCenter, Template: "breach_notification", Audience: AudienceBoth, DefaultWebUI: true, DefaultEmail: true, Required: true},
)

// buildCatalog indexes the event list by name and panics on a duplicate,
// which can only ever be a programming error in this file.
func buildCatalog(events ...Event) map[string]Event {
	out := make(map[string]Event, len(events))
	for _, e := range events {
		if _, dup := out[e.Name]; dup {
			panic("notify: duplicate catalog event " + e.Name)
		}
		out[e.Name] = e
	}
	return out
}

// Lookup returns the catalog entry for an event name.
func Lookup(name string) (Event, bool) {
	e, ok := catalog[name]
	return e, ok
}

// Events returns every catalog entry sorted by name.
func Events() []Event {
	out := make([]Event, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EventsByCategory returns the catalog entries in one category, sorted by
// name, for rendering a preference panel section.
func EventsByCategory(category string) []Event {
	var out []Event
	for _, e := range Events() {
		if e.Category == category {
			out = append(out, e)
		}
	}
	return out
}

// Categories returns every category present in the catalog, sorted.
func Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range catalog {
		if !seen[e.Category] {
			seen[e.Category] = true
			out = append(out, e.Category)
		}
	}
	sort.Strings(out)
	return out
}
