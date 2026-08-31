package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/config"
	apperrors "github.com/webappsgo/cashp/src/errors"
)

// webhookTestHost is a TEST-NET-3 literal from RFC 5737. It passes the SSRF
// guard without a DNS lookup, so the webhook tests never touch the network
// and never depend on a resolver being present.
const webhookTestHost = "https://198.51.100.10"

// captured is one outbound request recorded by the stub client.
type captured struct {
	url     string
	headers http.Header
	body    []byte
}

// stubDoer records outbound requests instead of dialing them, and answers
// with a configurable status so a rejected delivery can be exercised too.
type stubDoer struct {
	status   int
	failWith error
	requests []captured
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.requests = append(s.requests, captured{url: req.URL.String(), headers: req.Header.Clone(), body: body})

	if s.failWith != nil {
		return nil, s.failWith
	}

	status := s.status
	if status == 0 {
		status = http.StatusNoContent
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("receiver response")),
	}, nil
}

// last returns the most recent captured request, failing the test when the
// client was never called.
func (s *stubDoer) last(t *testing.T) captured {
	t.Helper()
	if len(s.requests) == 0 {
		t.Fatal("the webhook was never delivered")
	}
	return s.requests[len(s.requests)-1]
}

// contactsWithURL returns a contact source with one transport configured on
// the admin role, complete with the signing secret every delivery needs.
func contactsWithURL(transport, endpoint string) func() *config.ContactConfig {
	contacts := &config.ContactConfig{}
	role := contacts.Role(config.RoleAdmin)
	role.Email = "admin@example.com"
	role.Webhooks = map[string]string{}
	role.Webhooks[transport] = endpoint
	role.Webhooks[transport+config.WebhookSecretSuffix] = "0123456789abcdef"
	return func() *config.ContactConfig { return contacts }
}

// contactsForRoles configures the same transport on several roles, each with
// its own endpoint path so a delivery can be attributed to one role.
func contactsForRoles(transport string, roles ...config.ContactRoleName) func() *config.ContactConfig {
	contacts := &config.ContactConfig{}
	for _, name := range roles {
		role := contacts.Role(name)
		role.Email = string(name) + "@example.com"
		role.Webhooks = map[string]string{}
		role.Webhooks[transport] = webhookTestHost + "/hooks/" + string(name)
		role.Webhooks[transport+config.WebhookSecretSuffix] = "secret-" + string(name)
	}
	return func() *config.ContactConfig { return contacts }
}

// sampleRendered is the notification the webhook tests deliver.
func sampleRendered() Rendered {
	return Rendered{
		ID:        "0195b6a0-0000-7000-8000-00000000abcd",
		Event:     EventTest,
		Type:      TypeError,
		Subject:   "Disk almost full",
		Body:      "The /var partition is at 94 percent.\n",
		Link:      "https://panel.example.com/admin/storage",
		Role:      string(config.RoleAdmin),
		AppName:   "cashp",
		AppURL:    "https://panel.example.com",
		Version:   "1.2.3",
		CreatedAt: time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC),
	}
}

// newTestWebhook wires a channel for one transport onto a stub client.
func newTestWebhook(t *testing.T, transport, endpoint string, client *stubDoer) *WebhookChannel {
	t.Helper()
	channel, err := NewWebhookChannel(transport, contactsWithURL(transport, endpoint), client, time.Now)
	if err != nil {
		t.Fatalf("new %s channel: %v", transport, err)
	}
	channel.SetIdentity("cashp", "1.2.3", "https://panel.example.com")
	return channel
}

func TestTransportNamesCoverEveryBuiltInTransport(t *testing.T) {
	names := TransportNames()
	if len(names) != 7 {
		t.Fatalf("expected the seven PART 12 transports, got %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("transport names must be sorted, got %v", names)
		}
	}

	want := map[string]bool{
		TransportTelegram:   true,
		TransportDiscord:    true,
		TransportSlack:      true,
		TransportMattermost: true,
		TransportPushover:   true,
		TransportGotify:     true,
		TransportGeneric:    true,
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("unexpected transport %q", name)
		}
	}
}

func TestWebhookChannelsNeverAutoEnableAndAlwaysDocumentThemselves(t *testing.T) {
	for _, transport := range TransportNames() {
		channel := newTestWebhook(t, transport, webhookTestHost+"/hooks/admin", &stubDoer{})

		if channel.AutoEnable() {
			t.Fatalf("%s must wait for an operator to activate it", transport)
		}
		if channel.Name() != transport {
			t.Fatalf("expected name %q, got %q", transport, channel.Name())
		}

		schema := channel.ConfigSchema()
		if len(schema) == 0 {
			t.Fatalf("%s has no configuration schema", transport)
		}
		for _, field := range schema {
			if !field.Secret {
				t.Fatalf("%s field %q holds a credential-bearing URL and must be marked secret", transport, field.Name)
			}
			if field.Help == "" || field.Example == "" || field.Security == "" {
				t.Fatalf("%s field %q is missing its inline help text", transport, field.Name)
			}
			if field.EnvVar != "" {
				t.Fatalf("%s must not read an environment variable; only SMTP does", transport)
			}
		}

		help := channel.Help()
		if help.Summary == "" || len(help.Setup) == 0 || len(help.Troubleshooting) == 0 {
			t.Fatalf("%s help must be self-contained", transport)
		}
		if help.Comparison.Speed == "" || help.Comparison.Reliability == "" || help.Comparison.Pricing == "" {
			t.Fatalf("%s is missing its comparison row", transport)
		}
	}
}

func TestNewWebhookChannelRequiresAContactSource(t *testing.T) {
	if _, err := NewWebhookChannel(TransportSlack, nil, &stubDoer{}, time.Now); err == nil {
		t.Fatal("a webhook channel without contact configuration must not be constructed")
	}
}

func TestWebhookValidateRejectsAnUnconfiguredTransport(t *testing.T) {
	contacts := &config.ContactConfig{}
	channel, err := NewWebhookChannel(TransportSlack, func() *config.ContactConfig { return contacts }, &stubDoer{}, time.Now)
	if err != nil {
		t.Fatalf("new channel: %v", err)
	}

	if err := channel.Validate(); err == nil {
		t.Fatal("a transport with no configured role must fail validation")
	}
	if len(channel.Endpoints()) != 0 {
		t.Fatalf("expected no endpoints, got %v", channel.Endpoints())
	}
	if channel.Accepts(sampleRendered()) {
		t.Fatal("an unconfigured transport must not accept messages")
	}

	result := channel.Test(context.Background())
	if result.OK() {
		t.Fatal("testing an unconfigured transport must fail")
	}
}

func TestWebhookRefusesAPrivateDestination(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:9000/hook",
		"http://localhost:9000/hook",
		"https://10.0.0.5/hook",
		"http://169.254.169.254/latest/meta-data/",
		"https://gotify.internal/message",
	} {
		client := &stubDoer{}
		channel := newTestWebhook(t, TransportGeneric, endpoint, client)

		if err := channel.Validate(); err == nil {
			t.Fatalf("%s must fail validation", endpoint)
		}
		if err := channel.Send(context.Background(), sampleRendered()); err == nil {
			t.Fatalf("%s must not be delivered to", endpoint)
		}
		if len(client.requests) != 0 {
			t.Fatalf("%s: the SSRF guard must reject the destination before a socket is opened", endpoint)
		}
	}
}

func TestWebhookAcceptsAPublicDestination(t *testing.T) {
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", &stubDoer{})
	if err := channel.Validate(); err != nil {
		t.Fatalf("a routable HTTPS endpoint must validate: %v", err)
	}
	if !channel.Accepts(sampleRendered()) {
		t.Fatal("a configured transport must accept messages for its role")
	}
	if endpoints := channel.Endpoints(); len(endpoints) != 1 || endpoints[0] != string(config.RoleAdmin) {
		t.Fatalf("expected only the admin endpoint, got %v", endpoints)
	}
}

func TestWebhookSignsAndStampsEveryDelivery(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	if err := channel.Send(context.Background(), sampleRendered()); err != nil {
		t.Fatalf("send: %v", err)
	}

	request := client.last(t)
	signature := request.headers.Get(config.WebhookSignatureHeader)
	if !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("unexpected signature %q", signature)
	}
	if !config.VerifyWebhookSignature("0123456789abcdef", request.body, signature) {
		t.Fatal("the signature must cover exactly the bytes the receiver reads")
	}

	if got := request.headers.Get(config.WebhookEventHeader); got != EventTest {
		t.Fatalf("unexpected event header %q", got)
	}
	if got := request.headers.Get(config.WebhookIDHeader); got != sampleRendered().ID {
		t.Fatalf("the idempotency key must be the dispatch id, got %q", got)
	}
	if got := request.headers.Get("X-Notification-Source"); got != "cashp" {
		t.Fatalf("unexpected source header %q", got)
	}
	if got := request.headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("unexpected content type %q", got)
	}
	if !strings.Contains(request.headers.Get("User-Agent"), "1.2.3") {
		t.Fatalf("the user agent must carry the server version, got %q", request.headers.Get("User-Agent"))
	}

	stamp, err := strconv.ParseInt(request.headers.Get(config.WebhookTimestampHeader), 10, 64)
	if err != nil {
		t.Fatalf("timestamp header: %v", err)
	}
	if !config.VerifyWebhookTimestamp(time.Unix(stamp, 0), time.Now()) {
		t.Fatal("the delivery timestamp must fall inside the replay window")
	}
}

func TestWebhookRetryKeepsTheSameIdempotencyKey(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	message := sampleRendered()
	for attempt := 0; attempt < 3; attempt++ {
		if err := channel.Send(context.Background(), message); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected three attempts, got %d", len(client.requests))
	}

	first := client.requests[0].headers.Get(config.WebhookIDHeader)
	for _, request := range client.requests[1:] {
		if got := request.headers.Get(config.WebhookIDHeader); got != first {
			t.Fatalf("a retry must reuse the idempotency key %q, got %q", first, got)
		}
	}
}

func TestWebhookTelegramPayload(t *testing.T) {
	client := &stubDoer{}
	endpoint := webhookTestHost + "/bot123:AA/sendMessage?chat_id=-1001234567890"
	channel := newTestWebhook(t, TransportTelegram, endpoint, client)

	message := sampleRendered()
	if err := channel.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}

	request := client.last(t)
	parsed, err := url.Parse(request.url)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	query := parsed.Query()
	if query.Get("chat_id") != "-1001234567890" {
		t.Fatal("the operator's chat_id must survive untouched")
	}
	if query.Get("text") != plainMessage(message) {
		t.Fatalf("unexpected text parameter %q", query.Get("text"))
	}
	if query.Get("disable_web_page_preview") != "true" {
		t.Fatal("link previews must be disabled")
	}

	var payload map[string]string
	if err := json.Unmarshal(request.body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["text"] != plainMessage(message) {
		t.Fatalf("unexpected body %v", payload)
	}
}

func TestWebhookDiscordPayload(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, TransportDiscord, webhookTestHost+"/api/webhooks/1/tok", client)

	message := sampleRendered()
	if err := channel.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(client.last(t).body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["content"] != plainMessage(message) {
		t.Fatalf("unexpected content %q", payload["content"])
	}
	if payload["username"] != "cashp" {
		t.Fatalf("unexpected username %q", payload["username"])
	}
}

func TestWebhookSlackAndMattermostSharePayloadShape(t *testing.T) {
	message := sampleRendered()
	for _, transport := range []string{TransportSlack, TransportMattermost} {
		client := &stubDoer{}
		channel := newTestWebhook(t, transport, webhookTestHost+"/hooks/admin", client)

		if err := channel.Send(context.Background(), message); err != nil {
			t.Fatalf("%s send: %v", transport, err)
		}

		var payload map[string]string
		if err := json.Unmarshal(client.last(t).body, &payload); err != nil {
			t.Fatalf("%s decode: %v", transport, err)
		}
		if payload["text"] != plainMessage(message) {
			t.Fatalf("%s payload must carry a single text field, got %v", transport, payload)
		}
	}
}

func TestWebhookPushoverLiftsCredentialsIntoTheBody(t *testing.T) {
	client := &stubDoer{}
	endpoint := webhookTestHost + "/1/messages.json?token=apptoken&user=userkey"
	channel := newTestWebhook(t, TransportPushover, endpoint, client)

	message := sampleRendered()
	if err := channel.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(client.last(t).body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["token"] != "apptoken" || payload["user"] != "userkey" {
		t.Fatalf("the token and user key must reach the body, got %v", payload)
	}
	if payload["title"] != message.Subject {
		t.Fatalf("unexpected title %v", payload["title"])
	}
	if payload["url"] != message.Link {
		t.Fatalf("unexpected url %v", payload["url"])
	}
	if priority, ok := payload["priority"].(float64); !ok || int(priority) != 1 {
		t.Fatalf("an error notification must raise the pushover priority, got %v", payload["priority"])
	}
	if priority := pushoverPriority(TypeInfo); priority != -1 {
		t.Fatalf("an informational notification must stay quiet, got %d", priority)
	}
	if priority := pushoverPriority(TypeSecurity); priority != 1 {
		t.Fatalf("a security notification must be high priority, got %d", priority)
	}
}

func TestWebhookGotifyAddsTheMessagePathOnce(t *testing.T) {
	message := sampleRendered()
	for _, endpoint := range []string{
		webhookTestHost + "?token=abc",
		webhookTestHost + "/?token=abc",
		webhookTestHost + "/message?token=abc",
	} {
		client := &stubDoer{}
		channel := newTestWebhook(t, TransportGotify, endpoint, client)

		if err := channel.Send(context.Background(), message); err != nil {
			t.Fatalf("%s send: %v", endpoint, err)
		}

		request := client.last(t)
		parsed, err := url.Parse(request.url)
		if err != nil {
			t.Fatalf("parse target: %v", err)
		}
		if parsed.Path != "/message" {
			t.Fatalf("%s produced path %q", endpoint, parsed.Path)
		}
		if parsed.Query().Get("token") != "abc" {
			t.Fatalf("%s lost the application token", endpoint)
		}

		var payload map[string]any
		if err := json.Unmarshal(request.body, &payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if priority, ok := payload["priority"].(float64); !ok || int(priority) != 8 {
			t.Fatalf("an error notification must map to gotify priority 8, got %v", payload["priority"])
		}
	}

	if gotifyPriority(TypeSuccess) != 3 || gotifyPriority(TypeWarning) != 5 || gotifyPriority(TypeInfo) != 2 {
		t.Fatal("the gotify priority map does not follow the documented scale")
	}
}

func TestWebhookGenericEnvelopeCarriesTheDocumentedFields(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	message := sampleRendered()
	if err := channel.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}

	var payload genericPayload
	if err := json.Unmarshal(client.last(t).body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Role != string(config.RoleAdmin) || payload.Event != EventTest {
		t.Fatalf("unexpected routing fields %+v", payload)
	}
	if payload.Subject != message.Subject || payload.Severity != message.Severity() {
		t.Fatalf("unexpected content fields %+v", payload)
	}
	if payload.ProjectName != "cashp" || payload.ProjectVersion != "1.2.3" || payload.AppURL != message.AppURL {
		t.Fatalf("unexpected identity fields %+v", payload)
	}
	if payload.TrackingID != message.ID {
		t.Fatalf("unexpected tracking id %q", payload.TrackingID)
	}
	if _, err := time.Parse(time.RFC3339, payload.Timestamp); err != nil {
		t.Fatalf("timestamp must be RFC 3339: %v", err)
	}
}

func TestWebhookUnknownTransportFallsBackToTheGenericEnvelope(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, "acme", webhookTestHost+"/hooks/admin", client)

	if channel.Name() != "acme" {
		t.Fatalf("an operator-defined transport keeps its own name, got %q", channel.Name())
	}
	if channel.Category() != CategoryGeneric {
		t.Fatalf("unexpected category %q", channel.Category())
	}
	if schema := channel.ConfigSchema(); len(schema) != 1 || schema[0].Name != "acme" {
		t.Fatalf("the schema must name the operator's own key, got %v", schema)
	}

	if err := channel.Send(context.Background(), sampleRendered()); err != nil {
		t.Fatalf("send: %v", err)
	}
	var payload genericPayload
	if err := json.Unmarshal(client.last(t).body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Event != EventTest {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestWebhookRoleFallbackChain(t *testing.T) {
	cases := []struct {
		role       string
		configured []config.ContactRoleName
		want       string
	}{
		{role: string(config.RoleAdmin), configured: []config.ContactRoleName{config.RoleAdmin}, want: "/hooks/admin"},
		{role: string(config.RoleSecurity), configured: []config.ContactRoleName{config.RoleAdmin}, want: "/hooks/admin"},
		{role: string(config.RoleSecurity), configured: []config.ContactRoleName{config.RoleAdmin, config.RoleSecurity}, want: "/hooks/security"},
		{role: string(config.RoleAbuse), configured: []config.ContactRoleName{config.RoleAdmin, config.RoleGeneral}, want: "/hooks/general"},
		{role: string(config.RoleAbuse), configured: []config.ContactRoleName{config.RoleAdmin}, want: "/hooks/admin"},
		{role: string(config.RoleGeneral), configured: []config.ContactRoleName{config.RoleAdmin}, want: "/hooks/admin"},
		{role: "", configured: []config.ContactRoleName{config.RoleAdmin}, want: "/hooks/admin"},
	}

	for _, tc := range cases {
		client := &stubDoer{}
		channel, err := NewWebhookChannel(TransportGeneric, contactsForRoles(TransportGeneric, tc.configured...), client, time.Now)
		if err != nil {
			t.Fatalf("new channel: %v", err)
		}

		message := sampleRendered()
		message.Role = tc.role
		if err := channel.Send(context.Background(), message); err != nil {
			t.Fatalf("role %q: %v", tc.role, err)
		}
		if got := client.last(t).url; !strings.HasSuffix(got, tc.want) {
			t.Fatalf("role %q resolved to %q, want a %q endpoint", tc.role, got, tc.want)
		}
	}
}

func TestWebhookSignsWithTheResolvedRoleSecret(t *testing.T) {
	client := &stubDoer{}
	channel, err := NewWebhookChannel(TransportGeneric, contactsForRoles(TransportGeneric, config.RoleAdmin, config.RoleSecurity), client, time.Now)
	if err != nil {
		t.Fatalf("new channel: %v", err)
	}

	message := sampleRendered()
	message.Role = string(config.RoleSecurity)
	if err := channel.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}

	request := client.last(t)
	signature := request.headers.Get(config.WebhookSignatureHeader)
	if !config.VerifyWebhookSignature("secret-security", request.body, signature) {
		t.Fatal("the delivery must be signed with the resolved role's own secret")
	}
	if config.VerifyWebhookSignature("secret-admin", request.body, signature) {
		t.Fatal("the admin secret must not verify a security delivery")
	}
}

func TestWebhookNeverLeaksTheSecretIntoTheRequest(t *testing.T) {
	client := &stubDoer{}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	if err := channel.Send(context.Background(), sampleRendered()); err != nil {
		t.Fatalf("send: %v", err)
	}

	request := client.last(t)
	if strings.Contains(string(request.body), "0123456789abcdef") {
		t.Fatal("the signing secret must never appear in the payload")
	}
	for name, values := range request.headers {
		for _, value := range values {
			if strings.Contains(value, "0123456789abcdef") {
				t.Fatalf("header %s leaked the signing secret", name)
			}
		}
	}
}

func TestWebhookSurfacesAReceiverRejection(t *testing.T) {
	client := &stubDoer{status: http.StatusInternalServerError}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	err := channel.Send(context.Background(), sampleRendered())
	if err == nil {
		t.Fatal("a 500 response must be reported as a failed delivery")
	}
	if !RetryableDelivery(err) {
		t.Fatal("a server-side rejection is transient and must be retried")
	}

	result := channel.Test(context.Background())
	if result.OK() {
		t.Fatal("a rejected test delivery must not pass")
	}
	if !result.Connected {
		t.Fatal("the test result must record that the receiver was reached")
	}
}

func TestWebhookTestPassesAgainstAnAcceptingReceiver(t *testing.T) {
	client := &stubDoer{status: http.StatusOK}
	channel := newTestWebhook(t, TransportSlack, webhookTestHost+"/services/T/B/X", client)

	result := channel.Test(context.Background())
	if !result.OK() {
		t.Fatalf("expected a passing test, got %+v", result)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected exactly one test delivery, got %d", len(client.requests))
	}
	if got := client.last(t).headers.Get(config.WebhookEventHeader); got != EventTest {
		t.Fatalf("unexpected test event %q", got)
	}
}

func TestRetryableDeliveryClassification(t *testing.T) {
	if RetryableDelivery(nil) {
		t.Fatal("a successful delivery is not retryable")
	}
	if RetryableDelivery(ErrBlockedDestination) {
		t.Fatal("a blocked destination is a configuration fault and must not be retried")
	}
	if RetryableDelivery(apperrors.New(apperrors.CodeNotFound, http.StatusNotFound, "no webhook configured")) {
		t.Fatal("an unconfigured endpoint must not be retried")
	}
	if !RetryableDelivery(ErrDeliveryRejected) {
		t.Fatal("a rejected delivery must be retried")
	}
	if !RetryableDelivery(apperrors.New(apperrors.CodeUnavailable, http.StatusBadGateway, "connection reset")) {
		t.Fatal("a transport failure must be retried")
	}
}

func TestWebhookTransportFailureIsRetryable(t *testing.T) {
	client := &stubDoer{failWith: io.ErrUnexpectedEOF}
	channel := newTestWebhook(t, TransportGeneric, webhookTestHost+"/hooks/admin", client)

	err := channel.Send(context.Background(), sampleRendered())
	if err == nil {
		t.Fatal("a dial failure must be reported")
	}
	if !RetryableDelivery(err) {
		t.Fatal("a dial failure is transient and must be retried")
	}
}

func TestPlainMessageJoinsSubjectBodyAndLink(t *testing.T) {
	rendered := plainMessage(Rendered{Subject: "Subject line", Body: "  Body text  ", Link: "https://panel.example.com/x"})
	if !strings.HasPrefix(rendered, "Subject line\n\n") {
		t.Fatalf("the subject must lead the message, got %q", rendered)
	}
	if !strings.HasSuffix(rendered, "https://panel.example.com/x") {
		t.Fatalf("the deep link must close the message, got %q", rendered)
	}
	if strings.Contains(rendered, "  Body text  ") {
		t.Fatalf("the body must be trimmed, got %q", rendered)
	}

	bare := plainMessage(Rendered{Body: "just a body"})
	if bare != "just a body" {
		t.Fatalf("a bodyless notification must render cleanly, got %q", bare)
	}
}

func TestAppendQueryPreservesExistingParameters(t *testing.T) {
	target, err := appendQuery(webhookTestHost+"/send?chat_id=42", map[string]string{"text": "hello world"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Query().Get("chat_id") != "42" || parsed.Query().Get("text") != "hello world" {
		t.Fatalf("unexpected query %q", parsed.RawQuery)
	}

	if _, err := appendQuery("://not a url", nil); err == nil {
		t.Fatal("a malformed endpoint must be rejected")
	}
}
