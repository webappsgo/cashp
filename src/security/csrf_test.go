package security

import (
	"strings"
	"testing"
	"time"
)

// withCSRFClock replaces the CSRF clock for the duration of a test.
func withCSRFClock(t *testing.T, now func() time.Time) {
	t.Helper()

	original := csrfNow
	csrfNow = now
	t.Cleanup(func() { csrfNow = original })
}

func TestNewCSRFTokenValidates(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")

	token := NewCSRFToken(secret, "sess_abc123")
	if token == "" {
		t.Fatal("NewCSRFToken returned an empty token")
	}
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Fatalf("token %q must have three dot-separated parts", token)
	}

	if !ValidateCSRFToken(secret, "sess_abc123", token) {
		t.Fatal("a freshly minted token failed validation")
	}
}

func TestNewCSRFTokenUnique(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")

	first := NewCSRFToken(secret, "sess_abc123")
	second := NewCSRFToken(secret, "sess_abc123")

	if first == second {
		t.Fatal("two tokens for the same session must differ")
	}
	if !ValidateCSRFToken(secret, "sess_abc123", second) {
		t.Fatal("the second token failed validation")
	}
}

func TestValidateCSRFTokenRejects(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("fedcba9876543210fedcba9876543210")
	token := NewCSRFToken(secret, "sess_abc123")

	tampered := token[:len(token)-1] + "A"
	if strings.HasSuffix(token, "A") {
		tampered = token[:len(token)-1] + "B"
	}

	tests := []struct {
		name      string
		secret    []byte
		sessionID string
		token     string
	}{
		{"wrong secret", other, "sess_abc123", token},
		{"wrong session", secret, "sess_other", token},
		{"tampered mac", secret, "sess_abc123", tampered},
		{"missing parts", secret, "sess_abc123", "onlyonepart"},
		{"empty token", secret, "sess_abc123", ""},
		{"non numeric timestamp", secret, "sess_abc123", "notatime.nonce.mac"},
		{"empty nonce", secret, "sess_abc123", "1700000000..mac"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if ValidateCSRFToken(tc.secret, tc.sessionID, tc.token) {
				t.Fatal("token was accepted but must be rejected")
			}
		})
	}
}

func TestValidateCSRFTokenExpiry(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	withCSRFClock(t, func() time.Time { return base })
	token := NewCSRFToken(secret, "sess_abc123")

	withCSRFClock(t, func() time.Time { return base.Add(CSRFTokenTTL - time.Minute) })
	if !ValidateCSRFToken(secret, "sess_abc123", token) {
		t.Fatal("token inside its TTL was rejected")
	}

	withCSRFClock(t, func() time.Time { return base.Add(CSRFTokenTTL + time.Minute) })
	if ValidateCSRFToken(secret, "sess_abc123", token) {
		t.Fatal("expired token was accepted")
	}

	withCSRFClock(t, func() time.Time { return base.Add(-10 * time.Minute) })
	if ValidateCSRFToken(secret, "sess_abc123", token) {
		t.Fatal("token issued beyond the clock-skew allowance was accepted")
	}

	withCSRFClock(t, func() time.Time { return base.Add(-time.Minute) })
	if !ValidateCSRFToken(secret, "sess_abc123", token) {
		t.Fatal("token within the clock-skew allowance was rejected")
	}
}
