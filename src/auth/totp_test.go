package auth

import (
	"strings"
	"testing"
	"time"
)

func newTestTOTPSecret(t *testing.T) string {
	t.Helper()
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("NewTOTPSecret: %v", err)
	}
	return secret
}

func TestNewTOTPSecretIsUsableBase32(t *testing.T) {
	secret := newTestTOTPSecret(t)
	if secret == "" {
		t.Fatal("NewTOTPSecret returned empty string")
	}
	if strings.ContainsAny(secret, "=") {
		t.Errorf("secret must not be padded, got %q", secret)
	}
	if _, err := TOTPCode(secret, time.Now()); err != nil {
		t.Errorf("secret from NewTOTPSecret must decode: %v", err)
	}
}

func TestNewTOTPSecretIsRandom(t *testing.T) {
	a := newTestTOTPSecret(t)
	b := newTestTOTPSecret(t)
	if a == b {
		t.Error("two calls to NewTOTPSecret produced the same secret")
	}
}

func TestTOTPProvisioningURIContainsFields(t *testing.T) {
	uri := TOTPProvisioningURI("cashp", "alice", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{"otpauth://totp/", "cashp", "alice", "secret=JBSWY3DPEHPK3PXP", "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("provisioning URI %q missing %q", uri, want)
		}
	}
}

// TestTOTPCodeIsDeterministicRFC4226Vector uses the RFC 4226 / RFC 6238 test
// secret "12345678901234567890" (ASCII) base32-encoded, at T=59s (counter=1,
// period=30), which the RFC 6238 Appendix B table defines as producing an
// 8-digit code ending in "94287082"; this implementation truncates to 6
// digits, so the expected 6-digit code is the last 6 digits: "287082".
func TestTOTPCodeIsDeterministicRFC4226Vector(t *testing.T) {
	secret := totpEncoding.EncodeToString([]byte("12345678901234567890"))
	code, err := totpAt(secret, 1)
	if err != nil {
		t.Fatalf("totpAt: %v", err)
	}
	if code != "287082" {
		t.Errorf("totpAt(secret, 1) = %q, want %q", code, "287082")
	}
}

func TestTOTPCodeIsSixDigitsAndStable(t *testing.T) {
	secret := newTestTOTPSecret(t)
	now := time.Now()
	code1, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if len(code1) != 6 {
		t.Errorf("code length = %d, want 6", len(code1))
	}
	code2, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if code1 != code2 {
		t.Errorf("same secret+time produced different codes: %q vs %q", code1, code2)
	}
}

func TestValidateTOTPAcceptsCurrentCode(t *testing.T) {
	secret := newTestTOTPSecret(t)
	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if !ValidateTOTP(secret, code) {
		t.Error("ValidateTOTP rejected the currently valid code")
	}
}

func TestValidateTOTPAcceptsAdjacentSkewWindow(t *testing.T) {
	secret := newTestTOTPSecret(t)
	now := time.Now()
	prevCode, err := totpAt(secret, now.Unix()/totpPeriod-1)
	if err != nil {
		t.Fatalf("totpAt: %v", err)
	}
	if !ValidateTOTP(secret, prevCode) {
		t.Error("ValidateTOTP rejected a code from the previous time-step (should tolerate ±1 skew)")
	}
}

func TestValidateTOTPRejectsOutOfWindowCode(t *testing.T) {
	secret := newTestTOTPSecret(t)
	now := time.Now()
	farCode, err := totpAt(secret, now.Unix()/totpPeriod-5)
	if err != nil {
		t.Fatalf("totpAt: %v", err)
	}
	if ValidateTOTP(secret, farCode) {
		t.Error("ValidateTOTP accepted a code 5 steps outside the current window")
	}
}

func TestValidateTOTPRejectsWrongCode(t *testing.T) {
	secret := newTestTOTPSecret(t)
	if ValidateTOTP(secret, "000000") {
		// astronomically unlikely to collide; if it ever does, the test is flaky by design of TOTP itself
		code, _ := TOTPCode(secret, time.Now())
		if code == "000000" {
			t.Skip("random secret happened to produce 000000 for the current window")
		}
		t.Error("ValidateTOTP accepted an arbitrary wrong code")
	}
}

func TestValidateTOTPRejectsMalformedInput(t *testing.T) {
	secret := newTestTOTPSecret(t)
	cases := []string{"", "12345", "1234567", "abcdef"}
	for _, code := range cases {
		if ValidateTOTP(secret, code) {
			t.Errorf("ValidateTOTP(secret, %q) = true, want false", code)
		}
	}
	if ValidateTOTP("", "123456") {
		t.Error("ValidateTOTP with empty secret must return false")
	}
}
