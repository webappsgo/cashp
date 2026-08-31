package logging

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/security"
)

func TestRedactAttrMasksSensitiveNames(t *testing.T) {
	names := []string{"password", "api_key", "access_token", "session", "secret", "pwd", "totp_secret", "recovery_key"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got := redactAttr(nil, slog.String(name, "super-sensitive"))
			if got.Value.String() != security.MaskedValue {
				t.Fatalf("%s = %q, want %q", name, got.Value.String(), security.MaskedValue)
			}
			if got.Key != name {
				t.Fatalf("key was rewritten to %q", got.Key)
			}
		})
	}
}

func TestRedactAttrKeepsRequiredErrorFields(t *testing.T) {
	if got := redactAttr(nil, slog.String("error_code", "DB_TIMEOUT")); got.Value.String() != "DB_TIMEOUT" {
		t.Fatalf("error_code = %q, want DB_TIMEOUT", got.Value.String())
	}
	if got := redactAttr(nil, slog.String("request_id", "req_abc")); got.Value.String() != "req_abc" {
		t.Fatalf("request_id = %q, want req_abc", got.Value.String())
	}
	if got := redactAttr(nil, slog.Int("http_status", 500)); got.Value.Int64() != 500 {
		t.Fatalf("http_status = %v, want 500", got.Value)
	}
}

func TestRedactAttrHashesTokenValues(t *testing.T) {
	plaintext, hash, err := security.GenerateToken(security.PrefixAdmin)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	got := redactAttr(nil, slog.String("credential", plaintext))
	if strings.Contains(got.Value.String(), plaintext) {
		t.Fatal("the full token reached the log")
	}
	if got.Value.String() != hash {
		t.Fatalf("token value = %q, want its SHA-256 hash", got.Value.String())
	}
}

func TestRedactAttrMasksHashesAndPrivateKeys(t *testing.T) {
	pemBlock := "-----BEGIN RSA " + "PRIVATE KEY-----\nnot-real-material\n-----END RSA " + "PRIVATE KEY-----"

	values := []string{
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2g",
		"$2b$12$abcdefghijklmnopqrstuv",
		pemBlock,
	}

	for _, value := range values {
		got := redactAttr(nil, slog.String("blob", value))
		if got.Value.String() != security.MaskedValue {
			t.Fatalf("value %q was not masked, got %q", value, got.Value.String())
		}
	}
}

func TestRedactAttrRedactsURLQueryParams(t *testing.T) {
	got := redactAttr(nil, slog.String("url", "https://example.com/cb?code=abc123&state=ok"))

	if strings.Contains(got.Value.String(), "abc123") {
		t.Fatalf("url still contains the secret: %q", got.Value.String())
	}
	if !strings.Contains(got.Value.String(), "state=ok") {
		t.Fatalf("url lost a safe parameter: %q", got.Value.String())
	}
}

func TestRedactAttrStripsControlCharacters(t *testing.T) {
	got := redactAttr(nil, slog.String("msg", "injected\x00line\nbreak"))

	if strings.ContainsRune(got.Value.String(), 0) {
		t.Fatalf("null byte survived into the log: %q", got.Value.String())
	}
}

func TestRedactAttrLeavesNonStringsAlone(t *testing.T) {
	got := redactAttr(nil, slog.Bool("cluster_mode", true))
	if !got.Value.Bool() {
		t.Fatal("non-string attribute was altered")
	}
}

func TestAuditLogNeverCarriesSecrets(t *testing.T) {
	dir := initForTest(t, Options{Level: slog.LevelInfo, JSON: true})

	plaintext, _, err := security.GenerateToken(security.PrefixUser)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	Audit().Info("token.created",
		slog.String("password", "hunter2"),
		slog.String("api_key", "sk-live-abcdef"),
		slog.String("credential", plaintext),
	)

	content := readLog(t, dir, AuditLogName)
	for _, secret := range []string{"hunter2", "sk-live-abcdef", plaintext} {
		if strings.Contains(content, secret) {
			t.Fatalf("audit.log leaked %q: %s", secret, content)
		}
	}
}

func TestIsURLKey(t *testing.T) {
	for _, key := range []string{"url", "URI", "referer", "callback_url", "target_uri"} {
		if !isURLKey(key) {
			t.Fatalf("%q must be treated as a URL key", key)
		}
	}
	for _, key := range []string{"msg", "curl_command", "urlencoded"} {
		if isURLKey(key) {
			t.Fatalf("%q must not be treated as a URL key", key)
		}
	}
}
