package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/webappsgo/cashp/src/errors"
)

// ChannelWebUI is the identifier of the in-app channel.
const ChannelWebUI = "webui"

// ErrNoRecipient rejects a delivery with nothing to address it to.
var ErrNoRecipient = errors.New(errors.CodeValidation, http.StatusBadRequest, "notification has no recipient")

// WebUIChannel stores notifications in the bell-icon notification center.
// AI.md PART 18 -> "WebUI Notification System" makes this channel always
// available: it never dials out, so it works with no SMTP, no network and no
// third-party account.
type WebUIChannel struct {
	store *Store
	now   func() time.Time
}

// NewWebUIChannel returns the in-app channel backed by a store.
func NewWebUIChannel(store *Store, now func() time.Time) (*WebUIChannel, error) {
	if store == nil {
		return nil, errors.New(errors.CodeInternal, http.StatusInternalServerError, "webui channel needs a notification store")
	}
	if now == nil {
		now = time.Now
	}
	return &WebUIChannel{store: store, now: now}, nil
}

// Name returns the channel identifier.
func (c *WebUIChannel) Name() string { return ChannelWebUI }

// Category returns the in-app category.
func (c *WebUIChannel) Category() string { return CategoryInApp }

// AutoEnable reports true. The in-app channel has no credentials to get
// wrong, so a passing test activates it. Every outbound channel except SMTP
// returns false and waits for an administrator.
func (c *WebUIChannel) AutoEnable() bool { return true }

// Validate always succeeds once a store is attached; the constructor
// refuses to build the channel without one.
func (c *WebUIChannel) Validate() error {
	if c.store == nil {
		return errors.New(errors.CodeInternal, http.StatusInternalServerError, "webui channel has no notification store")
	}
	return nil
}

// Accepts reports whether the event is placed in the notification center.
// A toast-only event, such as a settings-saved confirmation, is rendered by
// the request that caused it and is never stored.
func (c *WebUIChannel) Accepts(r Rendered) bool {
	event, ok := Lookup(r.Event)
	if !ok {
		return true
	}
	return event.Surfaces.Has(SurfaceCenter)
}

// Test verifies the notification tables are reachable.
func (c *WebUIChannel) Test(ctx context.Context) TestResult {
	started := c.now()
	if err := c.Validate(); err != nil {
		return TestResult{Detail: "notification store is not configured", Err: err}
	}
	if err := c.store.db.Ping(ctx); err != nil {
		return TestResult{Detail: "notification database is unreachable", Err: err}
	}
	if _, err := c.store.UnreadCount(ctx, AudienceAdmin, ""); err != nil {
		return TestResult{
			Connected: true,
			Detail:    "notification tables are not readable",
			Err:       err,
		}
	}
	return TestResult{
		Connected:     true,
		Authenticated: true,
		Delivered:     true,
		Latency:       c.now().Sub(started),
		Detail:        "notification center is reachable",
	}
}

// Send stores one notification in the recipient's notification center.
func (c *WebUIChannel) Send(ctx context.Context, r Rendered) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if r.Recipient.ID == "" {
		return ErrNoRecipient.WithDetails(map[string]any{"channel": ChannelWebUI, "event": r.Event})
	}

	audience := r.Recipient.Audience
	if audience == "" || audience == AudienceBoth {
		audience = AudienceUser
	}

	surfaces := SurfaceCenter
	if event, ok := Lookup(r.Event); ok {
		surfaces = event.Surfaces
	}

	id := r.ID
	if id == "" {
		generated, err := NewID()
		if err != nil {
			return err
		}
		id = generated
	}

	created := r.CreatedAt
	if created.IsZero() {
		created = c.now()
	}

	return c.store.Insert(ctx, Record{
		ID:        id,
		Audience:  audience,
		OwnerID:   r.Recipient.ID,
		Event:     r.Event,
		Type:      r.Type,
		Surfaces:  surfaces,
		Title:     r.Subject,
		Body:      r.Body,
		Link:      r.Link,
		CreatedAt: created,
	})
}

// ConfigSchema returns the two display settings from PART 18's "Sane
// Defaults" table. The channel has no credentials, so nothing here is
// secret and nothing is read from the environment.
func (c *WebUIChannel) ConfigSchema() []Field {
	return []Field{
		{
			Name:        "position",
			Label:       "Toast position",
			Kind:        "select",
			Options:     []string{"top-right", "top-left", "bottom-right", "bottom-left"},
			Placeholder: "top-right",
			Help:        "Which corner of the screen transient toast notifications appear in.",
			Example:     "top-right",
		},
		{
			Name:        "duration",
			Label:       "Toast duration (seconds)",
			Kind:        "number",
			Placeholder: "5",
			Help:        "How long a toast stays on screen before it fades. Zero keeps it until the reader dismisses it. Errors and security alerts always require a manual dismiss regardless of this value.",
			Example:     "5",
		},
		{
			Name:        "retention_days",
			Label:       "Retention (days)",
			Kind:        "number",
			Placeholder: "30",
			Help:        "How long a stored notification stays in the notification center before the cleanup task removes it. Only the newest 100 notifications per account are kept regardless of age.",
			Example:     "30",
		},
	}
}

// Help returns the channel's embedded documentation.
func (c *WebUIChannel) Help() Help {
	return Help{
		Summary: "In-app notification center. Always available and never leaves the server.",
		Setup: []string{
			"Nothing to set up. The notification center is created with the database schema on first start.",
			"Open the bell icon in the header to read stored notifications.",
			"Adjust toast position and duration above if the defaults get in the way.",
		},
		Troubleshooting: []HelpEntry{
			{
				Symptom:    "The bell icon shows no notifications even though events are firing.",
				Resolution: "Check the notification preferences for the account: an event with its WebUI toggle off is stored for nobody. Security events cannot be switched off and always appear.",
			},
			{
				Symptom:    "Old notifications disappear sooner than expected.",
				Resolution: "Only the newest 100 notifications per account are kept, and anything older than the retention window is removed by the notification_cleanup task. Raise the retention value or rely on email for a permanent record.",
			},
			{
				Symptom:    "The channel test fails with a database error.",
				Resolution: "The notification tables are unreachable. Check the database connection on the server status page; the in-app channel has no other dependency.",
			},
		},
		Comparison: Comparison{
			Speed:           "instant",
			Reliability:     "high",
			RequiresAccount: false,
			Pricing:         "free",
		},
	}
}
