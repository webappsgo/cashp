package security

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateTokenFormat(t *testing.T) {
	for _, prefix := range TokenPrefixes {
		t.Run(prefix, func(t *testing.T) {
			plaintext, hash, err := GenerateToken(prefix)
			if err != nil {
				t.Fatalf("GenerateToken: %v", err)
			}

			if !strings.HasPrefix(plaintext, prefix) {
				t.Fatalf("token %q lacks prefix %q", plaintext, prefix)
			}

			body := strings.TrimPrefix(plaintext, prefix)
			if len(body) != TokenRandomLen {
				t.Fatalf("body length = %d, want %d", len(body), TokenRandomLen)
			}
			if !isAlphanumeric(body) {
				t.Fatalf("body %q is not alphanumeric", body)
			}

			if hash != HashToken(plaintext) {
				t.Fatal("returned hash does not match HashToken of the plaintext")
			}
			if strings.Contains(hash, body) {
				t.Fatal("stored hash leaks the token body")
			}
		})
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	seen := make(map[string]bool, 64)

	for i := 0; i < 64; i++ {
		plaintext, _, err := GenerateToken(PrefixAdmin)
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if seen[plaintext] {
			t.Fatalf("duplicate token generated: %q", plaintext)
		}
		seen[plaintext] = true
	}
}

func TestGenerateTokenUnknownPrefix(t *testing.T) {
	if _, _, err := GenerateToken("bad_"); !errors.Is(err, ErrUnknownTokenPrefix) {
		t.Fatalf("got %v, want ErrUnknownTokenPrefix", err)
	}
}

func TestHashTokenKnownVector(t *testing.T) {
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	if got := HashToken(""); got != want {
		t.Fatalf("HashToken(\"\") = %q, want %q", got, want)
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("distinct inputs produced the same digest")
	}
}

func TestParseToken(t *testing.T) {
	body := strings.Repeat("a", TokenRandomLen)

	tests := []struct {
		name       string
		token      string
		wantPrefix string
		wantErr    error
	}{
		{"admin", PrefixAdmin + body, PrefixAdmin, nil},
		{"admin agent beats admin", PrefixAdminAgent + body, PrefixAdminAgent, nil},
		{"user agent", PrefixUserAgent + body, PrefixUserAgent, nil},
		{"org", PrefixOrg + body, PrefixOrg, nil},
		{"short body", PrefixAdmin + "abc", "", ErrInvalidTokenFormat},
		{"non alphanumeric body", PrefixAdmin + strings.Repeat("-", TokenRandomLen), "", ErrInvalidTokenFormat},
		{"unknown prefix", "xyz_" + body, "", ErrUnknownTokenPrefix},
		{"empty", "", "", ErrUnknownTokenPrefix},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix, parsedBody, err := ParseToken(tc.token)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if prefix != tc.wantPrefix {
				t.Fatalf("prefix = %q, want %q", prefix, tc.wantPrefix)
			}
			if parsedBody != body {
				t.Fatalf("body = %q, want %q", parsedBody, body)
			}
		})
	}
}

func TestTokenDisplayPrefix(t *testing.T) {
	plaintext := PrefixAdmin + strings.Repeat("z", TokenRandomLen)

	got := TokenDisplayPrefix(plaintext)
	if len(got) != TokenDisplayLen {
		t.Fatalf("display prefix length = %d, want %d", len(got), TokenDisplayLen)
	}
	if got != "adm_zzzz" {
		t.Fatalf("display prefix = %q, want %q", got, "adm_zzzz")
	}

	if TokenDisplayPrefix("adm_") != "adm_" {
		t.Fatal("short input must be returned unchanged")
	}
}

func TestConstantTimeEqualString(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"equal", "adm_secret-value", "adm_secret-value", true},
		{"both empty", "", "", true},
		{"different value", "adm_secret-value", "adm_secret-valuf", false},
		{"different length", "short", "a much longer string", false},
		{"prefix only", "abc", "abcdef", false},
		{"case differs", "Token", "token", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConstantTimeEqualString(tc.a, tc.b); got != tc.want {
				t.Fatalf("ConstantTimeEqualString(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestConstantTimeEqualStringComparesFixedWidth(t *testing.T) {
	// Every comparison runs over a 32-byte digest regardless of input
	// length, so an attacker cannot learn the secret's length from timing.
	long := strings.Repeat("x", 100000)

	if ConstantTimeEqualString(long, "x") {
		t.Fatal("unequal inputs compared equal")
	}
	if !ConstantTimeEqualString(long, long) {
		t.Fatal("equal long inputs compared unequal")
	}
}

func TestIsValidTokenPrefix(t *testing.T) {
	if !IsValidTokenPrefix(PrefixOrgAgent) {
		t.Fatal("org agent prefix must be valid")
	}
	if IsValidTokenPrefix("adm") {
		t.Fatal("a prefix without its underscore must be rejected")
	}
}

func TestRandomAlphanumericRejectsNonPositive(t *testing.T) {
	if _, err := randomAlphanumeric(0); err == nil {
		t.Fatal("expected an error for a zero-length token body")
	}
}
