package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignWebhookMatchesHMACSHA256(t *testing.T) {
	body := []byte(`{"event":"security.breach_detected"}`)
	secret := "topsecret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if got := SignWebhook(secret, body); got != want {
		t.Errorf("SignWebhook() = %q, want %q", got, want)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	signature := SignWebhook("secret", body)

	if !VerifyWebhookSignature("secret", body, signature) {
		t.Error("a signature must verify against its own body and secret")
	}
	if !VerifyWebhookSignature("secret", body, "  "+signature+"  ") {
		t.Error("surrounding whitespace must not break verification")
	}
	if VerifyWebhookSignature("other", body, signature) {
		t.Error("a different secret must not verify")
	}
	if VerifyWebhookSignature("secret", []byte(`{"a":2}`), signature) {
		t.Error("a modified body must not verify")
	}
}

func TestVerifyWebhookTimestamp(t *testing.T) {
	now := time.Now()

	if !VerifyWebhookTimestamp(now.Add(-time.Minute), now) {
		t.Error("a recent timestamp must be accepted")
	}
	if !VerifyWebhookTimestamp(now.Add(time.Minute), now) {
		t.Error("small positive clock skew must be accepted")
	}
	if VerifyWebhookTimestamp(now.Add(-10*time.Minute), now) {
		t.Error("a stale timestamp must be rejected as a replay")
	}
	if VerifyWebhookTimestamp(now.Add(10*time.Minute), now) {
		t.Error("a far-future timestamp must be rejected")
	}
}

func TestNewWebhookIDIsUUIDv7(t *testing.T) {
	id, err := NewWebhookID()
	if err != nil {
		t.Fatalf("NewWebhookID() error = %v", err)
	}

	if len(id) != 36 {
		t.Fatalf("NewWebhookID() = %q, want a 36-character UUID", id)
	}
	for _, pos := range []int{8, 13, 18, 23} {
		if id[pos] != '-' {
			t.Fatalf("NewWebhookID() = %q, want dashes at the UUID positions", id)
		}
	}
	if id[14] != '7' {
		t.Errorf("version nibble = %c, want 7", id[14])
	}
	if !strings.ContainsRune("89ab", rune(id[19])) {
		t.Errorf("variant nibble = %c, want one of 89ab", id[19])
	}

	other, err := NewWebhookID()
	if err != nil {
		t.Fatalf("NewWebhookID() error = %v", err)
	}
	if other == id {
		t.Error("webhook IDs must be unique")
	}
}

func TestGenerateWebhookSecret(t *testing.T) {
	secret, err := GenerateWebhookSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSecret() error = %v", err)
	}
	if len(secret) != WebhookSecretSize*2 {
		t.Errorf("secret length = %d, want %d hex characters", len(secret), WebhookSecretSize*2)
	}

	other, err := GenerateWebhookSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSecret() error = %v", err)
	}
	if other == secret {
		t.Error("each generated secret must be distinct")
	}
}

func TestGenerateEncryptionKey(t *testing.T) {
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey() error = %v", err)
	}
	if key == "" {
		t.Fatal("GenerateEncryptionKey() returned an empty key")
	}
}

func TestNewWebhookDeliveryHeaders(t *testing.T) {
	body := []byte(`{"event":"test"}`)
	delivery, err := NewWebhookDelivery("slack", "https://hooks.example.test/x", "server.started", body, "secret", WebhookUserAgent("1.2.3", "https://panel.test"))
	if err != nil {
		t.Fatalf("NewWebhookDelivery() error = %v", err)
	}

	headers := delivery.Headers()
	if headers[WebhookSignatureHeader] != SignWebhook("secret", body) {
		t.Errorf("%s = %q, want the body signature", WebhookSignatureHeader, headers[WebhookSignatureHeader])
	}
	if headers[WebhookEventHeader] != "server.started" {
		t.Errorf("%s = %q, want server.started", WebhookEventHeader, headers[WebhookEventHeader])
	}
	if headers[WebhookIDHeader] != delivery.ID {
		t.Errorf("%s = %q, want %q", WebhookIDHeader, headers[WebhookIDHeader], delivery.ID)
	}
	if _, err := strconv.ParseInt(headers[WebhookTimestampHeader], 10, 64); err != nil {
		t.Errorf("%s = %q, want unix seconds", WebhookTimestampHeader, headers[WebhookTimestampHeader])
	}
	if headers["User-Agent"] != "cashp/1.2.3 (+https://panel.test)" {
		t.Errorf("User-Agent = %q", headers["User-Agent"])
	}
}

func TestNewWebhookDeliveryRequiresURLAndSecret(t *testing.T) {
	if _, err := NewWebhookDelivery("slack", "", "e", nil, "secret", ""); err == nil {
		t.Error("a webhook without a URL must be rejected")
	}
	if _, err := NewWebhookDelivery("slack", "https://x.test", "e", nil, "", ""); err == nil {
		t.Error("a webhook without a signing secret must be rejected")
	}
}

func TestWebhookUserAgentWithoutURL(t *testing.T) {
	if got := WebhookUserAgent("", ""); got != "cashp/devel" {
		t.Errorf("WebhookUserAgent() = %q, want cashp/devel", got)
	}
}

func TestWebhookRetryDelay(t *testing.T) {
	first, ok := WebhookRetryDelay(1)
	if !ok || first != time.Minute {
		t.Errorf("WebhookRetryDelay(1) = %v, %t, want 1m, true", first, ok)
	}

	last, ok := WebhookRetryDelay(len(WebhookRetryBackoff))
	if !ok || last != 24*time.Hour {
		t.Errorf("last retry delay = %v, %t, want 24h, true", last, ok)
	}

	if _, ok := WebhookRetryDelay(len(WebhookRetryBackoff) + 1); ok {
		t.Error("deliveries must be dropped after the last backoff step")
	}
	if _, ok := WebhookRetryDelay(0); ok {
		t.Error("attempt numbers are 1-based")
	}
}

func TestEnsureWebhookSecretIsStable(t *testing.T) {
	role := &ContactRole{Webhooks: map[string]string{"slack": "https://hooks.example.test/x"}}

	secret, err := role.EnsureWebhookSecret("slack")
	if err != nil {
		t.Fatalf("EnsureWebhookSecret() error = %v", err)
	}
	if role.Webhooks["slack"+WebhookSecretSuffix] != secret {
		t.Error("the generated secret must be stored beside the URL")
	}

	again, err := role.EnsureWebhookSecret("slack")
	if err != nil {
		t.Fatalf("EnsureWebhookSecret() error = %v", err)
	}
	if again != secret {
		t.Error("an existing secret must be reused, not rotated")
	}
}

func TestWebhookNamesExcludeSecretsAndEmpties(t *testing.T) {
	role := ContactRole{Webhooks: map[string]string{
		"slack":          "https://hooks.example.test/x",
		"slack_secret":   "abc",
		"discord":        "",
		"telegram":       "https://api.example.test/y",
		"generic_secret": "def",
	}}

	names := role.WebhookNames()
	if len(names) != 2 || names[0] != "slack" || names[1] != "telegram" {
		t.Errorf("WebhookNames() = %v, want [slack telegram]", names)
	}
	if role.WebhookURL("slack") != "https://hooks.example.test/x" {
		t.Errorf("WebhookURL(slack) = %q", role.WebhookURL("slack"))
	}
}

func TestContactFallbackChains(t *testing.T) {
	contact := &ContactConfig{
		Admin: ContactRole{Email: "admin@example.test"},
	}

	if got := contact.ResolveEmail(RoleSecurity); got != "admin@example.test" {
		t.Errorf("security email = %q, want the admin fallback", got)
	}
	if got := contact.ResolveEmail(RoleAbuse); got != "admin@example.test" {
		t.Errorf("abuse email = %q, want the admin fallback", got)
	}

	contact.General = ContactRole{Email: "hello@example.test"}
	if got := contact.ResolveEmail(RoleAbuse); got != "hello@example.test" {
		t.Errorf("abuse email = %q, want the general fallback before admin", got)
	}

	contact.Abuse = ContactRole{Email: "abuse@example.test"}
	if got := contact.ResolveEmail(RoleAbuse); got != "abuse@example.test" {
		t.Errorf("abuse email = %q, want the configured address", got)
	}
}

func TestContactEmailPlaceholderExpansion(t *testing.T) {
	contact := &ContactConfig{Admin: ContactRole{Email: "admin@{fqdn}"}}

	if got := contact.EmailFor(RoleAdmin, "panel.test"); got != "admin@panel.test" {
		t.Errorf("EmailFor() = %q, want admin@panel.test", got)
	}
}

func TestContactNeverInventsAbuseAddress(t *testing.T) {
	contact := &ContactConfig{}

	if got := contact.ResolveEmail(RoleAbuse); got != "" {
		t.Errorf("abuse email = %q, want an empty string when nothing is configured", got)
	}
	if contact.Role("nonexistent") != nil {
		t.Error("Role() must return nil for an unknown role")
	}
}

func TestResolveWebhooksFollowsFallback(t *testing.T) {
	contact := &ContactConfig{
		Admin: ContactRole{Webhooks: map[string]string{"slack": "https://hooks.example.test/admin"}},
	}

	targets := contact.ResolveWebhooks(RoleSecurity)
	if len(targets) != 1 || targets["slack"] != "https://hooks.example.test/admin" {
		t.Errorf("ResolveWebhooks(security) = %v, want the admin webhooks", targets)
	}

	contact.Security = ContactRole{Webhooks: map[string]string{"generic": "https://hooks.example.test/soc"}}
	targets = contact.ResolveWebhooks(RoleSecurity)
	if len(targets) != 1 || targets["generic"] != "https://hooks.example.test/soc" {
		t.Errorf("ResolveWebhooks(security) = %v, want the security webhooks", targets)
	}

	if targets := (&ContactConfig{}).ResolveWebhooks(RoleGeneral); targets != nil {
		t.Errorf("ResolveWebhooks() = %v, want nil when nothing is configured", targets)
	}
}
