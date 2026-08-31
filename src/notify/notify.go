// Package notify implements cashp's notification layer per AI.md PART 18
// (Email & Notifications) and PART 12 (Contact Configuration).
//
// Two delivery families exist. The WebUI family (toast, banner and the
// persistent notification center) is always available and never depends on
// outbound network access. The outbound family is a set of channel plugins:
// the SMTP email channel, which auto-enables once a live handshake with a
// mail server succeeds, and the webhook transports (telegram, discord,
// slack, mattermost, pushover, gotify and generic) configured per contact
// role under server.contact.
//
// Every channel implements Channel and is driven through the state machine
// in state.go, so nothing ever reaches ACTIVE without passing CONFIGURING
// and TESTING first. Every outbound HTTP destination is checked with
// security.ValidateOutboundURL before a socket is opened, including on
// redirect.
package notify

import (
	"strings"
	"time"
)

// Type is a WebUI notification type from AI.md PART 18 -> "WebUI
// Notification Types". The value doubles as the CSS modifier the frontend
// applies to a toast, banner or center entry.
type Type string

// The five notification types. Errors and security alerts never
// auto-dismiss because losing them loses the only record the user sees.
const (
	TypeSuccess  Type = "success"
	TypeInfo     Type = "info"
	TypeWarning  Type = "warning"
	TypeError    Type = "error"
	TypeSecurity Type = "security"
)

// AutoDismiss returns how long a toast of this type stays on screen. A zero
// duration means the user must dismiss it manually.
func (t Type) AutoDismiss() time.Duration {
	switch t {
	case TypeSuccess, TypeInfo:
		return 5 * time.Second
	case TypeWarning:
		return 10 * time.Second
	default:
		return 0
	}
}

// Valid reports whether t is one of the five defined notification types.
func (t Type) Valid() bool {
	switch t {
	case TypeSuccess, TypeInfo, TypeWarning, TypeError, TypeSecurity:
		return true
	default:
		return false
	}
}

// Severity maps a notification type onto the wire severity used by outbound
// webhook payloads and by incident receivers that grade alerts.
func (t Type) Severity() string {
	switch t {
	case TypeError:
		return "critical"
	case TypeSecurity:
		return "security"
	case TypeWarning:
		return "warning"
	default:
		return "info"
	}
}

// Surface is a bitmask of the WebUI placements an event uses, from the
// "Toast vs Banner vs Notification Center" table in PART 18.
type Surface uint8

// The three WebUI placements. An event may use any combination.
const (
	// SurfaceToast is transient corner feedback for an action just taken.
	SurfaceToast Surface = 1 << iota
	// SurfaceBanner is a persistent bar that stays until resolved.
	SurfaceBanner
	// SurfaceCenter stores the notification in the bell-icon history.
	SurfaceCenter
)

// Has reports whether s includes the given placement.
func (s Surface) Has(other Surface) bool { return s&other != 0 }

// String renders the placements as a stable comma-separated list for the
// admin panel and for test assertions.
func (s Surface) String() string {
	var parts []string
	if s.Has(SurfaceToast) {
		parts = append(parts, "toast")
	}
	if s.Has(SurfaceBanner) {
		parts = append(parts, "banner")
	}
	if s.Has(SurfaceCenter) {
		parts = append(parts, "center")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// Audience selects which of the two notification stores a record belongs
// to. AI.md PART 18 -> "Notification Storage" keeps server admins and
// regular users in separate tables.
type Audience string

// The two audiences. AudienceBoth is only valid on a catalog entry, never
// on a stored record.
const (
	AudienceAdmin Audience = "admin"
	AudienceUser  Audience = "user"
	AudienceBoth  Audience = "both"
)

// Includes reports whether a catalog audience covers a concrete one.
func (a Audience) Includes(other Audience) bool {
	return a == AudienceBoth || a == other
}

// Table returns the notification table backing this audience.
func (a Audience) Table() string {
	if a == AudienceAdmin {
		return "admin_notifications"
	}
	return "user_notifications"
}

// Recipient is one addressee of a dispatch. Email may be empty, in which
// case the recipient receives WebUI notifications only.
type Recipient struct {
	// Audience selects the admin or user notification store.
	Audience Audience
	// ID is the admin or user identifier owning the stored notification.
	ID string
	// Email is the address the SMTP channel delivers to.
	Email string
	// Username appears in account emails as {recipient_username}.
	Username string
	// Locale is reserved for per-recipient template selection; the default
	// language is used when empty.
	Locale string
}

// Message is one notification to dispatch. Event must name an entry in the
// catalog; everything else either overrides a catalog default or supplies
// template variables.
type Message struct {
	// Event is the catalog event name, for example "backup_failed".
	Event string
	// Title overrides the catalog title shown in the WebUI.
	Title string
	// Body is the WebUI message text. When empty the rendered email body's
	// first paragraph is used.
	Body string
	// Link is the in-app deep link the notification opens.
	Link string
	// Type overrides the catalog notification type.
	Type Type
	// Vars are the template variables merged over the global ones.
	Vars map[string]string
	// Recipients receive the WebUI notification and, when the event has an
	// email template, the email.
	Recipients []Recipient
	// Role selects which server.contact role's webhooks receive this
	// message. Empty means the admin role.
	Role string
	// ExecutionID groups notifications emitted by one scheduled run so a
	// specific failure event can suppress the generic scheduler_error.
	ExecutionID string
	// DedupKey suppresses repeat dispatches within DedupWindow. Empty
	// disables deduplication for this message.
	DedupKey string
	// DedupWindow is how long DedupKey stays claimed. Zero uses
	// DefaultDedupWindow.
	DedupWindow time.Duration
}

// DefaultDedupWindow is how long a claimed dedup key suppresses repeats
// when a message does not set its own window.
const DefaultDedupWindow = time.Hour

// Rendered is the fully resolved payload handed to a channel. Channels
// never see a Message: routing, preference resolution and template
// rendering all happen before this value is built.
type Rendered struct {
	// ID is the idempotency key shared by every attempt at this delivery.
	ID string
	// Event is the catalog event name.
	Event string
	// Type is the resolved notification type.
	Type Type
	// Subject is the rendered email subject, also used as the WebUI title.
	Subject string
	// Body is the rendered plain-text body.
	Body string
	// Link is the in-app deep link.
	Link string
	// Role is the contact role whose webhooks this message targets.
	Role string
	// Recipient is the addressee. Webhook channels ignore it.
	Recipient Recipient
	// Vars are the resolved template variables.
	Vars map[string]string
	// AppName is the branding title used in webhook payloads.
	AppName string
	// AppURL is the public base URL of this instance.
	AppURL string
	// Version is the running server version.
	Version string
	// CreatedAt is when the dispatch was created, not when it was retried.
	CreatedAt time.Time
}

// Severity returns the wire severity for outbound webhook payloads.
func (r Rendered) Severity() string { return r.Type.Severity() }
