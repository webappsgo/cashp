package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// The built-in webhook transport names from AI.md PART 12 -> "Webhook
// Transports". Any other key under a role's webhooks map is delivered by
// the generic adapter.
const (
	TransportTelegram   = "telegram"
	TransportDiscord    = "discord"
	TransportSlack      = "slack"
	TransportMattermost = "mattermost"
	TransportPushover   = "pushover"
	TransportGotify     = "gotify"
	TransportGeneric    = "generic"
)

// adapter turns a rendered notification into a concrete HTTP request for
// one transport. Every adapter returns a JSON body so the outbound
// signature covers exactly the bytes the receiver reads.
type adapter interface {
	// name is the transport key in the webhooks map.
	name() string
	// category groups the transport in the admin panel.
	category() string
	// build returns the final destination URL and the JSON body.
	build(endpoint string, r Rendered) (string, []byte, error)
	// schema describes the transport's configuration field.
	schema() []Field
	// help returns the transport's setup guide.
	help() Help
}

// adapters is the built-in adapter set, keyed by transport name.
var adapters = map[string]adapter{
	TransportTelegram:   telegramAdapter{},
	TransportDiscord:    discordAdapter{},
	TransportSlack:      slackAdapter{},
	TransportMattermost: mattermostAdapter{},
	TransportPushover:   pushoverAdapter{},
	TransportGotify:     gotifyAdapter{},
	TransportGeneric:    genericAdapter{},
}

// TransportNames returns the built-in transport names in sorted order.
func TransportNames() []string {
	out := make([]string, 0, len(adapters))
	for name := range adapters {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

// sortStrings sorts a string slice in place. It exists so this file does
// not import sort for a single call site alongside the registry's own.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// WebhookChannel delivers notifications through one transport. One channel
// instance is registered per transport name, and it resolves the endpoint
// for the message's contact role at dispatch time so an operator can change
// a URL without a restart.
type WebhookChannel struct {
	mu       sync.RWMutex
	adapter  adapter
	client   httpDoer
	contacts func() *config.ContactConfig
	appName  string
	version  string
	appURL   string
	now      func() time.Time
}

// NewWebhookChannel returns a channel for one transport. contacts must
// return the live contact configuration; client may be nil, in which case a
// hardened outbound client is built.
func NewWebhookChannel(transport string, contacts func() *config.ContactConfig, client httpDoer, now func() time.Time) (*WebhookChannel, error) {
	impl, ok := adapters[transport]
	if !ok {
		impl = genericAdapter{transport: transport}
	}
	if contacts == nil {
		return nil, errors.New(errors.CodeInternal, http.StatusInternalServerError, "webhook channel needs a contact configuration source")
	}
	if client == nil {
		client = newOutboundClient()
	}
	if now == nil {
		now = time.Now
	}
	return &WebhookChannel{adapter: impl, client: client, contacts: contacts, now: now}, nil
}

// SetIdentity records the values used in the outbound User-Agent and in the
// generic payload.
func (c *WebhookChannel) SetIdentity(appName, version, appURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appName, c.version, c.appURL = appName, version, appURL
}

// Name implements Channel.
func (c *WebhookChannel) Name() string { return c.adapter.name() }

// Category implements Channel.
func (c *WebhookChannel) Category() string { return c.adapter.category() }

// AutoEnable implements Channel. Only SMTP self-enables; a webhook waits
// for an operator to activate it after a passing test.
func (c *WebhookChannel) AutoEnable() bool { return false }

// Accepts implements Channel: a webhook carries server-side events, and
// only when the message's contact role has this transport configured.
func (c *WebhookChannel) Accepts(r Rendered) bool {
	_, _, err := c.endpoint(r.Role)
	return err == nil
}

// Endpoints returns the roles that have this transport configured directly
// (not merely reachable through another role's fallback), for the admin
// panel's per-role test buttons.
func (c *WebhookChannel) Endpoints() []string {
	contacts := c.contacts()
	if contacts == nil {
		return nil
	}
	name := c.adapter.name()
	var out []string
	for _, role := range []config.ContactRoleName{config.RoleAdmin, config.RoleSecurity, config.RoleAbuse, config.RoleGeneral} {
		target := contacts.Role(role)
		if target == nil {
			continue
		}
		if target.WebhookURL(name) != "" {
			out = append(out, string(role))
		}
	}
	return out
}

// Validate implements Channel. A transport with no configured URL on any
// role is simply not set up, which is a validation failure rather than a
// delivery failure.
func (c *WebhookChannel) Validate() error {
	roles := c.Endpoints()
	if len(roles) == 0 {
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "no contact role has this webhook transport configured")
	}
	for _, role := range roles {
		endpoint, _, err := c.endpoint(role)
		if err != nil {
			return err
		}
		if err := security.ValidateOutboundURL(endpoint); err != nil {
			return ErrBlockedDestination.WithDetails(map[string]any{"role": role, "reason": err.Error()})
		}
	}
	return nil
}

// Test implements Channel. It delivers the test notification through every
// role that has this transport configured, which is what the admin panel's
// per-tab test button drives.
func (c *WebhookChannel) Test(ctx context.Context) TestResult {
	start := c.now()
	result := TestResult{}

	roles := c.Endpoints()
	if len(roles) == 0 {
		result.Detail = "no contact role has this transport configured"
		result.Err = errors.New(errors.CodeValidation, http.StatusBadRequest, "webhook transport is not configured")
		return result
	}

	c.mu.RLock()
	appName, appURL, version := c.appName, c.appURL, c.version
	c.mu.RUnlock()

	sample := Rendered{
		Event:     EventTest,
		Type:      TypeInfo,
		Subject:   "Test notification from " + appName,
		Body:      "If you are reading this, " + appName + " can reach this webhook.",
		Role:      roles[0],
		AppName:   appName,
		AppURL:    appURL,
		Version:   version,
		CreatedAt: start,
	}
	if id, err := config.NewWebhookID(); err == nil {
		sample.ID = id
	}

	if err := c.Send(ctx, sample); err != nil {
		result.Connected = true
		result.Latency = c.now().Sub(start)
		result.Detail = "delivery to the " + roles[0] + " endpoint was not accepted"
		result.Err = err
		return result
	}

	result.Connected = true
	result.Authenticated = true
	result.Delivered = true
	result.Latency = c.now().Sub(start)
	result.Detail = "delivered to the " + roles[0] + " endpoint"
	return result
}

// Send implements Channel. Every delivery is signed with the role's
// per-webhook secret and carries the idempotency headers, so a retry the
// receiver has already handled can be discarded on their side.
func (c *WebhookChannel) Send(ctx context.Context, r Rendered) error {
	endpoint, secret, err := c.endpoint(r.Role)
	if err != nil {
		return err
	}

	target, body, err := c.adapter.build(endpoint, r)
	if err != nil {
		return err
	}

	c.mu.RLock()
	appName, version, appURL := c.appName, c.version, c.appURL
	c.mu.RUnlock()

	event := r.Event
	if event == "" {
		event = EventTest
	}
	delivery, err := config.NewWebhookDelivery(c.adapter.name(), target, event, body, secret, config.WebhookUserAgent(version, appURL))
	if err != nil {
		return errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "build webhook delivery")
	}
	if r.ID != "" {
		delivery.ID = r.ID
	}

	headers := delivery.Headers()
	if appName != "" {
		headers["X-Notification-Source"] = headerSafe(appName)
	}
	return postJSON(ctx, c.client, target, body, headers)
}

// ConfigSchema implements Channel.
func (c *WebhookChannel) ConfigSchema() []Field { return c.adapter.schema() }

// Help implements Channel.
func (c *WebhookChannel) Help() Help { return c.adapter.help() }

// endpoint resolves the URL and signing secret for a contact role, applying
// the fallback chain in AI.md PART 12 -> "Resolution Order Per Role".
func (c *WebhookChannel) endpoint(role string) (endpoint, secret string, err error) {
	contacts := c.contacts()
	if contacts == nil {
		return "", "", errors.New(errors.CodeUnavailable, http.StatusServiceUnavailable, "contact configuration is not loaded")
	}

	name := c.adapter.name()
	for _, candidate := range roleChain(role) {
		target := contacts.Role(candidate)
		if target == nil {
			continue
		}
		if raw := target.WebhookURL(name); raw != "" {
			return raw, target.Webhooks[name+config.WebhookSecretSuffix], nil
		}
	}
	return "", "", errors.New(errors.CodeNotFound, http.StatusNotFound, "no webhook configured for this contact role").
		WithDetails(map[string]any{"transport": name, "role": role})
}

// roleChain returns the role fallback order for a requested role. Security
// falls back to admin; abuse falls back to general then admin; general
// falls back to admin.
func roleChain(role string) []config.ContactRoleName {
	switch config.ContactRoleName(role) {
	case config.RoleSecurity:
		return []config.ContactRoleName{config.RoleSecurity, config.RoleAdmin}
	case config.RoleAbuse:
		return []config.ContactRoleName{config.RoleAbuse, config.RoleGeneral, config.RoleAdmin}
	case config.RoleGeneral:
		return []config.ContactRoleName{config.RoleGeneral, config.RoleAdmin}
	default:
		return []config.ContactRoleName{config.RoleAdmin}
	}
}

// plainMessage renders the single text block the chat transports carry.
func plainMessage(r Rendered) string {
	var b strings.Builder
	if r.Subject != "" {
		b.WriteString(r.Subject)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(r.Body))
	if r.Link != "" {
		b.WriteString("\n\n")
		b.WriteString(r.Link)
	}
	return strings.TrimSpace(b.String())
}

// appendQuery adds query parameters to an endpoint without disturbing the
// ones the operator already put there, such as a Telegram chat_id.
func appendQuery(endpoint string, values map[string]string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "webhook url is not valid")
	}
	query := parsed.Query()
	for key, value := range values {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// telegramAdapter posts to a Bot API sendMessage URL that already carries
// the chat_id, adding the message text as a query parameter.
type telegramAdapter struct{}

func (telegramAdapter) name() string     { return TransportTelegram }
func (telegramAdapter) category() string { return CategoryChat }

func (telegramAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	target, err := appendQuery(endpoint, map[string]string{"text": plainMessage(r), "disable_web_page_preview": "true"})
	if err != nil {
		return "", nil, err
	}
	body, err := json.Marshal(map[string]string{"text": plainMessage(r)})
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode telegram payload")
	}
	return target, body, nil
}

func (telegramAdapter) schema() []Field {
	return []Field{{
		Name: TransportTelegram, Label: "Telegram bot URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://api.telegram.org/bot<token>/sendMessage?chat_id=<id>",
		Help:        "Full Bot API sendMessage URL including the bot token and the destination chat_id. The server appends the message text.",
		Example:     "https://api.telegram.org/bot123456:AA.../sendMessage?chat_id=-1001234567890",
		Security:    "The URL contains the bot token; it is stored encrypted and never shown in full after saving.",
	}}
}

func (telegramAdapter) help() Help {
	return Help{
		Summary: "Sends messages to a Telegram chat through a bot.",
		Setup: []string{
			"Open a chat with @BotFather, send /newbot and follow the prompts to get a bot token.",
			"Add the bot to the destination group or channel and give it permission to post.",
			"Send any message in the chat, then open https://api.telegram.org/bot<token>/getUpdates and read the chat id from the response.",
			"Paste the full sendMessage URL with the chat_id query parameter into this field, then press Test.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "401 Unauthorized", Resolution: "The bot token is wrong or was revoked. Ask @BotFather for the current token."},
			{Symptom: "400 chat not found", Resolution: "The chat id is wrong, or the bot was never added to the chat. Re-read it from getUpdates."},
			{Symptom: "403 bot was blocked", Resolution: "A member removed the bot or blocked it. Re-add it and grant posting permission."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: true, Pricing: "free"},
	}
}

// discordAdapter posts a plain content message to a Discord webhook.
type discordAdapter struct{}

func (discordAdapter) name() string     { return TransportDiscord }
func (discordAdapter) category() string { return CategoryChat }

func (discordAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	payload := map[string]string{"content": plainMessage(r)}
	if r.AppName != "" {
		payload["username"] = r.AppName
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode discord payload")
	}
	return endpoint, body, nil
}

func (discordAdapter) schema() []Field {
	return []Field{{
		Name: TransportDiscord, Label: "Discord webhook URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://discord.com/api/webhooks/<id>/<token>",
		Help:        "Channel webhook URL. Create it under Server Settings, Integrations, Webhooks.",
		Example:     "https://discord.com/api/webhooks/123456789/AbCdEf...",
		Security:    "Anyone holding this URL can post to the channel; it is stored encrypted and never shown in full after saving.",
	}}
}

func (discordAdapter) help() Help {
	return Help{
		Summary: "Posts messages into a Discord channel through a channel webhook.",
		Setup: []string{
			"Open the destination channel, choose Edit Channel, then Integrations, then Webhooks.",
			"Create a webhook, name it, and copy the webhook URL.",
			"Paste the URL into this field and press Test.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "404 Unknown Webhook", Resolution: "The webhook was deleted or the URL is truncated. Create a new one and paste the full URL."},
			{Symptom: "429 Too Many Requests", Resolution: "Discord limits a webhook to about five posts a second. Reduce how many events route to this transport."},
			{Symptom: "400 Bad Request", Resolution: "The message exceeded 2000 characters. Shorten the template body."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: true, Pricing: "free"},
	}
}

// slackAdapter posts a Slack incoming-webhook text payload.
type slackAdapter struct{}

func (slackAdapter) name() string     { return TransportSlack }
func (slackAdapter) category() string { return CategoryChat }

func (slackAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	body, err := json.Marshal(map[string]string{"text": plainMessage(r)})
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode slack payload")
	}
	return endpoint, body, nil
}

func (slackAdapter) schema() []Field {
	return []Field{{
		Name: TransportSlack, Label: "Slack webhook URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://hooks.slack.com/services/<workspace-id>/<channel-id>/<token>",
		Help:        "Incoming Webhook URL from your Slack app. Each URL is bound to one channel.",
		Example:     "https://hooks.slack.com/services/<workspace-id>/<channel-id>/<token>",
		Security:    "Anyone holding this URL can post to the channel; it is stored encrypted and never shown in full after saving.",
	}}
}

func (slackAdapter) help() Help {
	return Help{
		Summary: "Posts messages into a Slack channel through an Incoming Webhook.",
		Setup: []string{
			"Go to api.slack.com/apps and create an app in your workspace.",
			"Open Incoming Webhooks and turn the feature on.",
			"Choose Add New Webhook to Workspace and pick the destination channel.",
			"Copy the generated webhook URL into this field and press Test.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "404 no_service", Resolution: "The webhook was revoked or the app was removed. Generate a new webhook URL."},
			{Symptom: "403 invalid_token", Resolution: "The URL was copied incompletely. Re-copy the whole URL including all three path segments."},
			{Symptom: "Messages go to the wrong channel", Resolution: "Each webhook URL is bound to one channel. Create a separate webhook for the channel you want."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: true, Pricing: "freemium"},
	}
}

// mattermostAdapter reuses the Slack-compatible body Mattermost accepts.
type mattermostAdapter struct{}

func (mattermostAdapter) name() string     { return TransportMattermost }
func (mattermostAdapter) category() string { return CategoryChat }

func (mattermostAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	return slackAdapter{}.build(endpoint, r)
}

func (mattermostAdapter) schema() []Field {
	return []Field{{
		Name: TransportMattermost, Label: "Mattermost webhook URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://mattermost.example.com/hooks/<id>",
		Help:        "Incoming Webhook URL from your Mattermost server. The payload format matches Slack.",
		Example:     "https://mattermost.example.com/hooks/abcdefghijklmnopqrstuvwxyz",
		Security:    "Anyone holding this URL can post to the channel; it is stored encrypted and never shown in full after saving.",
	}}
}

func (mattermostAdapter) help() Help {
	return Help{
		Summary: "Posts messages into a Mattermost channel through an Incoming Webhook.",
		Setup: []string{
			"In Mattermost, open the Integrations page for your team and choose Incoming Webhooks.",
			"Add an incoming webhook, pick the destination channel and save.",
			"Copy the generated URL into this field and press Test.",
			"If the server is on a private network, note that this panel refuses to POST to private addresses.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "webhook destination is not allowed", Resolution: "The URL resolves to a private or loopback address, which is blocked. Publish the Mattermost server on a routable name."},
			{Symptom: "403 Invalid webhook", Resolution: "Incoming webhooks are disabled for the team, or the hook was deleted. Re-enable them in the System Console."},
			{Symptom: "certificate errors", Resolution: "The Mattermost certificate is not trusted by this host. Install the issuing CA in the system trust store."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: false, Pricing: "free"},
	}
}

// pushoverAdapter posts a Pushover message, carrying the token and user key
// from the configured URL into the JSON body when they are present there.
type pushoverAdapter struct{}

func (pushoverAdapter) name() string     { return TransportPushover }
func (pushoverAdapter) category() string { return CategoryPush }

func (pushoverAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "pushover url is not valid")
	}

	query := parsed.Query()
	payload := map[string]any{
		"title":   r.Subject,
		"message": plainMessage(r),
	}
	if token := query.Get("token"); token != "" {
		payload["token"] = token
	}
	if user := query.Get("user"); user != "" {
		payload["user"] = user
	}
	if r.Link != "" {
		payload["url"] = r.Link
	}
	payload["priority"] = pushoverPriority(r.Type)

	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode pushover payload")
	}
	return endpoint, body, nil
}

// pushoverPriority maps a notification type onto Pushover's priority scale.
// Priority 2 is deliberately unused: it demands acknowledgement and would
// keep alerting an operator until they tap the notification.
func pushoverPriority(t Type) int {
	switch t {
	case TypeError, TypeSecurity:
		return 1
	case TypeWarning:
		return 0
	default:
		return -1
	}
}

func (pushoverAdapter) schema() []Field {
	return []Field{{
		Name: TransportPushover, Label: "Pushover API URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://api.pushover.net/1/messages.json?token=<app>&user=<key>",
		Help:        "Pushover messages endpoint with the application token and the user or group key as query parameters.",
		Example:     "https://api.pushover.net/1/messages.json?token=aznvyz...&user=uQiRzp...",
		Security:    "The URL contains the application token and user key; it is stored encrypted and never shown in full after saving.",
	}}
}

func (pushoverAdapter) help() Help {
	return Help{
		Summary: "Sends push notifications to Pushover clients on phone, tablet and desktop.",
		Setup: []string{
			"Sign in at pushover.net and copy your user key from the dashboard.",
			"Create an application under Your Applications to get an API token.",
			"Build the URL https://api.pushover.net/1/messages.json?token=<token>&user=<key> and paste it here.",
			"Press Test; the notification should arrive on every device signed in to that account.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "400 application token is invalid", Resolution: "The token belongs to a deleted application. Create a new application and use its token."},
			{Symptom: "400 user identifier is not a valid user", Resolution: "The user key is wrong, or you used the application token in the user field."},
			{Symptom: "Nothing arrives on the phone", Resolution: "The device is not registered to that user key, or the app is disabled in the account. Re-register the device."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: true, Pricing: "paid"},
	}
}

// gotifyAdapter posts to a self-hosted Gotify server's message endpoint.
type gotifyAdapter struct{}

func (gotifyAdapter) name() string     { return TransportGotify }
func (gotifyAdapter) category() string { return CategoryPush }

func (gotifyAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "gotify url is not valid")
	}
	// The operator may paste either the server root or the full message
	// endpoint; both must end up posting to /message.
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/message") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/message"
	}

	payload := map[string]any{
		"title":    r.Subject,
		"message":  plainMessage(r),
		"priority": gotifyPriority(r.Type),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode gotify payload")
	}
	return parsed.String(), body, nil
}

// gotifyPriority maps a notification type onto Gotify's 0 to 10 scale.
func gotifyPriority(t Type) int {
	switch t {
	case TypeError, TypeSecurity:
		return 8
	case TypeWarning:
		return 5
	case TypeSuccess:
		return 3
	default:
		return 2
	}
}

func (gotifyAdapter) schema() []Field {
	return []Field{{
		Name: TransportGotify, Label: "Gotify URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://gotify.example.com/message?token=<app-token>",
		Help:        "Your Gotify server URL with an application token. The /message path is added automatically if you omit it.",
		Example:     "https://gotify.example.com/message?token=AbCdEfGhIjKlMnO",
		Security:    "The URL contains an application token; it is stored encrypted and never shown in full after saving.",
	}}
}

func (gotifyAdapter) help() Help {
	return Help{
		Summary: "Sends push notifications through a self-hosted Gotify server.",
		Setup: []string{
			"Sign in to your Gotify server as an administrator.",
			"Open Apps and create an application; copy its token.",
			"Enter https://<your-gotify-host>/message?token=<token> in this field.",
			"Press Test; the message appears in the Gotify web client and any connected app.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "401 unauthorized", Resolution: "The token is a client token rather than an application token, or it was revoked. Create an application token."},
			{Symptom: "webhook destination is not allowed", Resolution: "The Gotify host resolves to a private address, which is blocked. Publish it on a routable name."},
			{Symptom: "404 page not found", Resolution: "A reverse proxy is stripping the path. Make sure /message reaches the Gotify server unchanged."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "medium", RequiresAccount: false, Pricing: "free"},
	}
}

// genericAdapter posts the documented JSON envelope to any HTTPS receiver.
// It also backs any transport name the operator invents, since AI.md leaves
// the webhooks map open.
type genericAdapter struct {
	// transport overrides the adapter name for an operator-defined
	// transport key.
	transport string
}

func (a genericAdapter) name() string {
	if a.transport != "" {
		return a.transport
	}
	return TransportGeneric
}

func (genericAdapter) category() string { return CategoryGeneric }

// genericPayload is the JSON envelope AI.md PART 12 defines for the generic
// transport.
type genericPayload struct {
	Role           string `json:"role"`
	Event          string `json:"event"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	Severity       string `json:"severity"`
	Timestamp      string `json:"timestamp"`
	ProjectName    string `json:"project_name"`
	ProjectVersion string `json:"project_version"`
	AppURL         string `json:"app_url"`
	TrackingID     string `json:"tracking_id,omitempty"`
}

func (a genericAdapter) build(endpoint string, r Rendered) (string, []byte, error) {
	timestamp := r.CreatedAt
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	payload := genericPayload{
		Role:           r.Role,
		Event:          r.Event,
		Subject:        r.Subject,
		Body:           r.Body,
		Severity:       r.Severity(),
		Timestamp:      timestamp.UTC().Format(time.RFC3339),
		ProjectName:    r.AppName,
		ProjectVersion: r.Version,
		AppURL:         r.AppURL,
		TrackingID:     r.ID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "encode webhook payload")
	}
	return endpoint, body, nil
}

func (a genericAdapter) schema() []Field {
	return []Field{{
		Name: a.name(), Label: "Webhook URL", Kind: "url", Required: true, Secret: true,
		Placeholder: "https://receiver.example.com/hooks/cashp",
		Help:        "Any HTTPS endpoint. It receives a JSON envelope with role, event, subject, body, severity, timestamp and version fields.",
		Example:     "https://receiver.example.com/hooks/cashp",
		Security:    "Signed with a per-webhook secret shown once when the URL is saved; verify X-Webhook-Signature on your side.",
	}}
}

func (a genericAdapter) help() Help {
	return Help{
		Summary: "POSTs a signed JSON envelope to any HTTPS receiver you control.",
		Setup: []string{
			"Stand up an HTTPS endpoint that accepts POST with a JSON body.",
			"Paste its URL here and save. The panel generates a signing secret and shows it once.",
			"Store that secret on the receiving side.",
			"Verify each request: compute HMAC-SHA256 of the raw body with the secret and compare it to the X-Webhook-Signature header in constant time.",
			"Reject requests whose X-Webhook-Timestamp is more than five minutes from your clock, and deduplicate on X-Webhook-ID.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "Signature never matches", Resolution: "Hash the raw request body before any JSON parsing or re-encoding, and compare against the value after the sha256= prefix."},
			{Symptom: "webhook destination is not allowed", Resolution: "The URL is plain HTTP or resolves to a private address. Use HTTPS on a public name."},
			{Symptom: "Duplicate deliveries", Resolution: "That is a retry after a non-2xx response. Deduplicate on X-Webhook-ID, which stays the same across retries."},
		},
		Comparison: Comparison{Speed: "instant", Reliability: "high", RequiresAccount: false, Pricing: "free"},
	}
}
