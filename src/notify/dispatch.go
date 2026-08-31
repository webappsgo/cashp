package notify

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/errors"
)

// Scheduler task names registered by this package. The scheduler owns the
// cadence; these are the identifiers it binds to Retry and Cleanup.
const (
	// TaskRetry drains the delivery retry queue.
	TaskRetry = "notification_retry"
	// TaskCleanup prunes stored notifications and expired dedup claims.
	TaskCleanup = "notification_cleanup"
)

// retryBatch is how many due deliveries one retry run drains.
const retryBatch = 50

// executionWindow is how long a per-execution suppression marker lives. A
// scheduled run that has not reported within this window is finished as far
// as suppression is concerned.
const executionWindow = time.Hour

// startupTestTimeout bounds the in-app channel's startup reachability test.
const startupTestTimeout = 5 * time.Second

// Options configures a Notifier.
type Options struct {
	// DB is the open database handle backing the stores.
	DB *database.DB
	// ConfigDir is the configuration root; custom email templates live in
	// its template/email subdirectory.
	ConfigDir string
	// AppName is the application title used in template variables.
	AppName string
	// AppURL is the canonical base URL.
	AppURL string
	// FQDN is the server's fully qualified domain name.
	FQDN string
	// OnionURL is the Tor address, empty when Tor is off.
	OnionURL string
	// I2PURL is the I2P address, empty when I2P is off.
	I2PURL string
	// AdminEmail is the Reply-To address for outbound email.
	AdminEmail string
	// Version is the running build version, reported in webhook payloads.
	Version string
	// SMTP is the stored SMTP configuration.
	SMTP SMTPSettings
	// Contacts returns the live contact configuration for webhook routing.
	// It is called once per dispatch so an administrator's change takes
	// effect without a restart.
	Contacts func() *config.ContactConfig
	// Dialer opens SMTP connections. Nil uses a plain bounded TCP dialer.
	Dialer func(ctx context.Context, network, address string) (net.Conn, error)
	// HTTPClient performs webhook deliveries. Nil builds the hardened
	// outbound client.
	HTTPClient httpDoer
	// Now supplies the clock. Nil uses time.Now.
	Now func() time.Time
}

// Notifier is the package facade. Callers hold one instance and call Notify;
// everything else - templates, channel state, preferences, dedup, retries -
// is resolved inside.
type Notifier struct {
	opts      Options
	store     *Store
	templates *Templates
	registry  *Registry
	smtp      *SMTPChannel
	webui     *WebUIChannel
	now       func() time.Time
}

// New builds a Notifier with every channel registered. The WebUI channel is
// activated immediately because it has nothing to configure; SMTP and every
// webhook transport start DISABLED and reach ACTIVE only through the
// CONFIGURING and TESTING states.
func New(opts Options) (*Notifier, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	store, err := NewStore(opts.DB, opts.Now)
	if err != nil {
		return nil, err
	}
	webui, err := NewWebUIChannel(store, opts.Now)
	if err != nil {
		return nil, err
	}

	client := opts.HTTPClient
	if client == nil {
		client = newOutboundClient()
	}
	contacts := opts.Contacts
	if contacts == nil {
		contacts = func() *config.ContactConfig { return nil }
	}

	n := &Notifier{
		opts:      opts,
		store:     store,
		templates: NewTemplates(CustomTemplateDir(opts.ConfigDir), opts.Now),
		registry:  NewRegistry(opts.Now),
		smtp:      NewSMTPChannel(opts.SMTP, opts.Dialer, opts.Now),
		webui:     webui,
		now:       opts.Now,
	}

	if err := n.registry.Register(webui); err != nil {
		return nil, err
	}
	if err := n.registry.Register(n.smtp); err != nil {
		return nil, err
	}
	for _, transport := range TransportNames() {
		channel, err := NewWebhookChannel(transport, contacts, client, opts.Now)
		if err != nil {
			return nil, err
		}
		channel.SetIdentity(opts.AppName, opts.Version, opts.AppURL)
		if err := n.registry.Register(channel); err != nil {
			return nil, err
		}
	}

	// The in-app channel is the one channel with no credentials and no
	// network dependency, so it walks its own state machine at startup.
	if err := n.registry.Configure(ChannelWebUI); err != nil {
		return nil, err
	}
	startupCtx, cancel := context.WithTimeout(context.Background(), startupTestTimeout)
	defer cancel()
	// A database that is not ready yet leaves the channel in TESTING rather
	// than failing startup; the next TestChannel call activates it.
	_, _ = n.registry.Test(startupCtx, ChannelWebUI)

	return n, nil
}

// Store exposes the persistence layer to the admin panel and the HTTP
// handlers.
func (n *Notifier) Store() *Store { return n.store }

// Templates exposes the email template set to the template admin panel.
func (n *Notifier) Templates() *Templates { return n.templates }

// Registry exposes the channel registry to the channel admin panel.
func (n *Notifier) Registry() *Registry { return n.registry }

// SMTP exposes the email channel so the config panel can read its masked
// settings and write new ones.
func (n *Notifier) SMTP() *SMTPChannel { return n.smtp }

// TaskNames returns the scheduler task identifiers this package expects to
// be run periodically.
func (n *Notifier) TaskNames() []string { return []string{TaskRetry, TaskCleanup} }

// ConfigureSMTP applies new SMTP settings and walks the channel back to
// CONFIGURING so it must pass a test before it sends again.
func (n *Notifier) ConfigureSMTP(ctx context.Context, settings SMTPSettings) error {
	n.smtp.Configure(settings)
	if err := n.registry.Configure(ChannelSMTP); err != nil {
		return err
	}
	return n.store.Audit(ctx, AuditEntry{
		Action:  ActionConfigChange,
		Channel: ChannelSMTP,
		Result:  "configured",
		Detail:  "smtp settings replaced; host " + settings.Host,
	})
}

// DetectSMTP runs the local relay sweep from PART 18 and applies the first
// relay that answers. It is a no-op when the channel is already configured.
func (n *Notifier) DetectSMTP(ctx context.Context, gatewayIP, globalIPv4 string) bool {
	settings := n.smtp.Settings()
	if settings.Host != "" {
		return false
	}
	host, port, ok := DetectSMTP(ctx, DetectionHosts(gatewayIP, n.opts.FQDN, globalIPv4), n.opts.Dialer)
	if !ok {
		// No relay answered. Email simply stays off; PART 18 treats this as
		// a normal outcome, not a startup failure.
		return false
	}
	detected := settings
	detected.Host = host
	detected.Port = port
	detected.TLS = TLSAuto
	detected.Detected = true
	if detected.FromEmail == "" && n.opts.FQDN != "" {
		detected.FromEmail = "no-reply@" + n.opts.FQDN
	}
	if detected.FromName == "" {
		detected.FromName = n.opts.AppName
	}
	n.smtp.Configure(detected)
	return true
}

// TestChannel runs one channel's live test and records the outcome. SMTP
// activates itself on a pass; every other outbound channel stays in TESTING
// until an administrator calls Registry.Activate.
func (n *Notifier) TestChannel(ctx context.Context, name string) (TestResult, error) {
	result, err := n.registry.Test(ctx, name)
	if err != nil {
		return result, err
	}
	outcome := "passed"
	if !result.OK() {
		outcome = "failed"
	}
	detail := result.Detail
	if result.Err != nil {
		detail = errors.From(result.Err).Message
	}
	if auditErr := n.store.Audit(ctx, AuditEntry{
		Action:  ActionChannelState,
		Channel: name,
		Result:  outcome,
		Detail:  detail,
	}); auditErr != nil {
		return result, auditErr
	}
	return result, nil
}

// Notify dispatches one message to one or more recipients. It is the only
// entry point other packages use.
func (n *Notifier) Notify(ctx context.Context, msg Message, recipients ...Recipient) error {
	event, ok := Lookup(msg.Event)
	if !ok {
		return ErrUnknownEvent.WithDetails(map[string]any{"event": msg.Event})
	}

	suppressed, err := n.suppressed(ctx, event, msg.ExecutionID)
	if err != nil {
		return err
	}
	if suppressed {
		return n.store.Audit(ctx, AuditEntry{
			Action: ActionDispatch,
			Event:  event.Name,
			Result: "suppressed",
			Detail: "a related failure already reported this execution",
		})
	}

	claimed, err := n.store.ClaimDedup(ctx, n.dedupKey(event, msg, recipients), event.Name, msg.DedupWindow)
	if err != nil {
		return err
	}
	if !claimed {
		return n.store.Audit(ctx, AuditEntry{
			Action: ActionDispatch,
			Event:  event.Name,
			Result: "deduplicated",
			Detail: "an identical notification is still inside its dedup window",
		})
	}

	if err := n.markExecution(ctx, event, msg.ExecutionID); err != nil {
		return err
	}

	var failure error
	for _, recipient := range recipients {
		if !event.Audience.Includes(recipient.Audience) {
			continue
		}
		if err := n.deliverTo(ctx, event, msg, recipient); err != nil && failure == nil {
			failure = err
		}
	}

	if event.Webhook {
		if err := n.fanOutWebhooks(ctx, event, msg); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

// deliverTo routes one message to one recipient across the in-app and email
// channels, honouring that recipient's preferences.
func (n *Notifier) deliverTo(ctx context.Context, event Event, msg Message, recipient Recipient) error {
	wantWebUI, wantEmail, err := n.store.Routing(ctx, recipient.Audience, recipient.ID, event.Name)
	if err != nil {
		return err
	}

	rendered, err := n.render(event, msg, recipient)
	if err != nil {
		return err
	}

	var failure error
	if wantWebUI && n.webui.Accepts(rendered) {
		if err := n.queueAndSend(ctx, ChannelWebUI, "", rendered); err != nil && failure == nil {
			failure = err
		}
	}
	if wantEmail && event.Template != "" && recipient.Email != "" {
		if state, err := n.registry.State(ChannelSMTP); err == nil && (state == StateActive || state == StateDegraded) {
			if err := n.queueAndSend(ctx, ChannelSMTP, "", rendered); err != nil && failure == nil {
				failure = err
			}
		}
	}
	return failure
}

// fanOutWebhooks delivers a server-level event to every active webhook
// transport. Per-user account events never reach this path.
func (n *Notifier) fanOutWebhooks(ctx context.Context, event Event, msg Message) error {
	rendered, err := n.render(event, msg, Recipient{Audience: AudienceAdmin})
	if err != nil {
		return err
	}
	role := msg.Role
	if role == "" {
		role = string(config.RoleGeneral)
	}
	rendered.Role = role

	var failure error
	for _, name := range n.registry.Active() {
		if name == ChannelWebUI || name == ChannelSMTP {
			continue
		}
		channel, err := n.registry.Channel(name)
		if err != nil || !channel.Accepts(rendered) {
			continue
		}
		if err := n.queueAndSend(ctx, name, role, rendered); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

// queueAndSend records the delivery, attempts it once and either completes
// it or hands it to the retry queue. Nothing is ever dropped silently: a
// delivery that cannot be retried is marked failed and stays in the log.
func (n *Notifier) queueAndSend(ctx context.Context, channel, role string, rendered Rendered) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(rendered)
	if err != nil {
		return errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode notification payload")
	}

	delivery := Delivery{
		ID:             id,
		NotificationID: rendered.ID,
		Event:          rendered.Event,
		Channel:        channel,
		Role:           role,
		Recipient:      rendered.Recipient.Email,
		Status:         StatusPending,
		NextAttemptAt:  n.now(),
		Payload:        string(payload),
	}
	if err := n.store.Enqueue(ctx, delivery); err != nil {
		return err
	}
	return n.attempt(ctx, delivery, rendered)
}

// attempt performs one delivery attempt and records its outcome.
func (n *Notifier) attempt(ctx context.Context, delivery Delivery, rendered Rendered) error {
	sendErr := n.registry.Deliver(ctx, delivery.Channel, rendered)
	if sendErr == nil {
		if err := n.store.Complete(ctx, delivery.ID); err != nil {
			return err
		}
		return n.store.Audit(ctx, AuditEntry{
			Action:  ActionDeliver,
			Channel: delivery.Channel,
			Event:   delivery.Event,
			Result:  StatusSent,
		})
	}

	message := errors.From(sendErr).Message
	if !RetryableDelivery(sendErr) {
		if err := n.store.Suppress(ctx, delivery.ID, message); err != nil {
			return err
		}
		return n.store.Audit(ctx, AuditEntry{
			Action:  ActionDeliver,
			Channel: delivery.Channel,
			Event:   delivery.Event,
			Result:  "rejected",
			Detail:  message,
		})
	}

	if err := n.store.Reschedule(ctx, delivery.ID, delivery.Attempt+1, sendErr); err != nil {
		return err
	}
	return n.store.Audit(ctx, AuditEntry{
		Action:  ActionDeliver,
		Channel: delivery.Channel,
		Event:   delivery.Event,
		Result:  "retry",
		Detail:  message,
	})
}

// Retry drains the due deliveries. It is the body of the notification_retry
// scheduler task and is safe to run concurrently: a delivery already
// completed by another node is skipped because its status is no longer
// pending when the next run reads it.
func (n *Notifier) Retry(ctx context.Context) error {
	due, err := n.store.Due(ctx, retryBatch)
	if err != nil {
		return err
	}

	var failure error
	for _, delivery := range due {
		var rendered Rendered
		if err := json.Unmarshal([]byte(delivery.Payload), &rendered); err != nil {
			// The payload cannot be replayed, so the delivery is closed out
			// rather than retried forever against a value nothing can read.
			if suppressErr := n.store.Suppress(ctx, delivery.ID, "stored payload is unreadable"); suppressErr != nil && failure == nil {
				failure = suppressErr
			}
			continue
		}
		if err := n.attempt(ctx, delivery, rendered); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

// Cleanup prunes stored notifications and expired dedup claims. It is the
// body of the notification_cleanup scheduler task.
func (n *Notifier) Cleanup(ctx context.Context) error {
	if err := n.store.Prune(ctx); err != nil {
		return err
	}
	return n.store.PruneDedup(ctx)
}

// SendTest delivers the test notification through one channel, which is
// what the admin panel's per-channel test button uses once the connectivity
// check has passed.
func (n *Notifier) SendTest(ctx context.Context, channel string, recipient Recipient) error {
	event, _ := Lookup(EventTest)
	rendered, err := n.render(event, Message{
		Event: EventTest,
		Title: "Test notification",
		Body:  "This is a test notification from " + n.opts.AppName + ".",
	}, recipient)
	if err != nil {
		return err
	}
	rendered.Role = string(config.RoleAdmin)
	return n.queueAndSend(ctx, channel, rendered.Role, rendered)
}

// render turns a catalog event and a caller message into the value every
// channel consumes. Email content comes from the template set; the WebUI
// title and body fall back to the catalog defaults.
func (n *Notifier) render(event Event, msg Message, recipient Recipient) (Rendered, error) {
	now := n.now()
	id, err := NewID()
	if err != nil {
		return Rendered{}, err
	}

	vars := GlobalVars(n.opts.AppName, n.opts.AppURL, n.opts.FQDN, n.opts.OnionURL, n.opts.I2PURL, n.opts.AdminEmail, now)
	vars["recipient_email"] = recipient.Email
	vars["recipient_username"] = recipient.Username
	for key, value := range msg.Vars {
		vars[key] = value
	}

	kind := msg.Type
	if kind == "" || !kind.Valid() {
		kind = event.Type
	}

	rendered := Rendered{
		ID:        id,
		Event:     event.Name,
		Type:      kind,
		Subject:   firstNonEmpty(msg.Title, event.Title),
		Body:      msg.Body,
		Link:      msg.Link,
		Role:      msg.Role,
		Recipient: recipient,
		Vars:      vars,
		AppName:   n.opts.AppName,
		AppURL:    n.opts.AppURL,
		Version:   n.opts.Version,
		CreatedAt: now,
	}

	if event.Template != "" {
		subject, body, err := n.templates.Render(event.Template, vars)
		if err != nil {
			// A missing or broken template must not stop the in-app
			// notification, so the caller-supplied text is kept instead.
			if !errors.Is(err, errors.CodeNotFound) {
				return Rendered{}, err
			}
		} else {
			rendered.Subject = subject
			rendered.Body = body
		}
	}
	if strings.TrimSpace(rendered.Body) == "" {
		rendered.Body = rendered.Subject
	}
	return rendered, nil
}

// dedupKey builds the deduplication key for a dispatch: the caller's own
// key when it supplied one, otherwise the event plus its recipients.
func (n *Notifier) dedupKey(event Event, msg Message, recipients []Recipient) string {
	if msg.DedupKey != "" {
		return event.Name + ":" + msg.DedupKey
	}
	parts := make([]string, 0, len(recipients)+2)
	parts = append(parts, event.Name, msg.ExecutionID)
	for _, recipient := range recipients {
		parts = append(parts, string(recipient.Audience)+"/"+recipient.ID)
	}
	return strings.Join(parts, ":")
}

// suppressed reports whether a related failure already covered this
// execution. PART 18 requires one notification per failed run: a backup
// failure or a certificate renewal failure cancels the generic scheduler
// error for the same execution.
func (n *Notifier) suppressed(ctx context.Context, event Event, executionID string) (bool, error) {
	if executionID == "" || len(event.SuppressedBy) == 0 {
		return false, nil
	}
	for _, other := range event.SuppressedBy {
		held, err := n.store.DedupHeld(ctx, executionMarker(executionID, other))
		if err != nil {
			return false, err
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

// markExecution records that this event reported for this execution, so a
// later generic failure notification for the same run is suppressed.
func (n *Notifier) markExecution(ctx context.Context, event Event, executionID string) error {
	if executionID == "" {
		return nil
	}
	if _, err := n.store.ClaimDedup(ctx, executionMarker(executionID, event.Name), event.Name, executionWindow); err != nil {
		return err
	}
	return nil
}

// executionMarker builds the per-execution suppression key.
func executionMarker(executionID, event string) string {
	return "exec:" + executionID + ":" + event
}

// firstNonEmpty returns the first non-empty value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
