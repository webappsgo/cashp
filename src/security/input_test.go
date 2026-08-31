package security

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	base := filepath.Join("var", "lib", "cashp", "uploads")

	tests := []struct {
		name      string
		untrusted string
		want      string
		wantErr   error
	}{
		{"simple file", "avatar.png", filepath.Join(base, "avatar.png"), nil},
		{"nested file", "tenant/1/avatar.png", filepath.Join(base, "tenant", "1", "avatar.png"), nil},
		{"dot segment", "./avatar.png", filepath.Join(base, "avatar.png"), nil},
		{"interior traversal stays inside", "tenant/../avatar.png", filepath.Join(base, "avatar.png"), nil},
		{"empty resolves to base", "", base, nil},
		{"parent traversal", "../secrets.yml", "", ErrPathEscapesBase},
		{"deep traversal", "a/b/../../../../etc/passwd", "", ErrPathEscapesBase},
		{"absolute path", "/etc/passwd", "", ErrPathNotRelative},
		{"null byte", "avatar.png\x00.txt", "", ErrPathNullByte},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SafeJoin(base, tc.untrusted)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got != tc.want {
				t.Fatalf("SafeJoin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSafeJoinEmptyBase(t *testing.T) {
	if _, err := SafeJoin("", "file.txt"); !errors.Is(err, ErrEmptyBase) {
		t.Fatalf("err = %v, want ErrEmptyBase", err)
	}
}

func TestSafeJoinSiblingPrefixIsRejected(t *testing.T) {
	// "uploads-private" shares a string prefix with "uploads" but is a
	// different directory and must not be reachable.
	if _, err := SafeJoin("/srv/uploads", "../uploads-private/key.pem"); !errors.Is(err, ErrPathEscapesBase) {
		t.Fatalf("err = %v, want ErrPathEscapesBase", err)
	}
}

func TestIsPrivateOrLoopbackIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.5", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"192.0.0.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::1", true},
		{"fe80::1", true},
		{"fd00::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse %q", tc.ip)
			}
			if got := IsPrivateOrLoopbackIP(ip); got != tc.want {
				t.Fatalf("IsPrivateOrLoopbackIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}

	if !IsPrivateOrLoopbackIP(nil) {
		t.Fatal("a nil IP must be treated as not routable")
	}
}

func TestValidateOutboundURLRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"file scheme", "file:///etc/passwd", ErrSchemeNotAllowed},
		{"gopher scheme", "gopher://example.com/", ErrSchemeNotAllowed},
		{"no scheme", "example.com/path", ErrSchemeNotAllowed},
		{"no host", "https://", ErrInvalidURL},
		{"loopback literal", "http://127.0.0.1:8080/admin", ErrHostNotAllowed},
		{"ipv6 loopback literal", "http://[::1]/admin", ErrHostNotAllowed},
		{"link local metadata", "http://169.254.169.254/latest/meta-data/", ErrHostNotAllowed},
		{"private range", "https://10.0.0.5/internal", ErrHostNotAllowed},
		{"localhost name", "https://localhost/admin", ErrHostNotAllowed},
		{"dot local", "https://db.local/", ErrHostNotAllowed},
		{"dot internal", "https://db.internal/", ErrHostNotAllowed},
		{"onion", "https://abcdefg.onion/", ErrHostNotAllowed},
		{"i2p", "https://abcdefg.b32.i2p/", ErrHostNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateOutboundURL(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"env pair", "DB_PASSWORD=hunter2", "DB_PASSWORD=" + MaskedValue},
		{"yaml pair", "api_key: abc123", "api_key: " + MaskedValue},
		{"header pair", "Authorization:Bearer abc123", "Authorization:" + MaskedValue},
		{"bare value", "abc123", MaskedValue},
		{"empty", "", ""},
		{"leading separator", "=abc123", MaskedValue},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskSecret(tc.in)
			if got != tc.want {
				t.Fatalf("MaskSecret(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.in != "" && strings.Contains(got, "hunter2") {
				t.Fatal("masked output still contains the secret value")
			}
		})
	}
}

func TestIsSensitiveName(t *testing.T) {
	sensitive := []string{"password", "API_KEY", "access_token", "session", "Secret", "user_api_key", "pwd"}
	for _, name := range sensitive {
		if !IsSensitiveName(name) {
			t.Fatalf("%q must be treated as sensitive", name)
		}
	}

	safe := []string{"request_id", "http_status", "username", "path", "method"}
	for _, name := range safe {
		if IsSensitiveName(name) {
			t.Fatalf("%q must not be treated as sensitive", name)
		}
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL("https://example.com/callback?code=abc123&state=xyz&access_token=zzz")

	if strings.Contains(got, "abc123") || strings.Contains(got, "zzz") {
		t.Fatalf("RedactURL left a secret in %q", got)
	}
	if !strings.Contains(got, "state=xyz") {
		t.Fatalf("RedactURL dropped a non-sensitive parameter: %q", got)
	}
	if !strings.Contains(got, "https://example.com/callback") {
		t.Fatalf("RedactURL mangled the base URL: %q", got)
	}

	if RedactURL("https://example.com/plain") != "https://example.com/plain" {
		t.Fatal("a URL with no query must pass through unchanged")
	}
	if RedactURL("://not a url") != MaskedValue {
		t.Fatal("an unparseable URL must be masked entirely")
	}
}

func TestEscapeHTML(t *testing.T) {
	got := EscapeHTML("<script>alert('x')</script>")

	if strings.Contains(got, "<script>") {
		t.Fatalf("EscapeHTML left live markup in %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("EscapeHTML = %q, want escaped angle brackets", got)
	}
}

func TestStripControlChars(t *testing.T) {
	got := StripControlChars("line\x00one\x1b[31mred\ttab\nnewline")

	if strings.ContainsRune(got, 0) || strings.ContainsRune(got, 0x1b) {
		t.Fatalf("control characters survived in %q", got)
	}
	if !strings.Contains(got, "\t") || !strings.Contains(got, "\n") {
		t.Fatalf("tab and newline must be preserved, got %q", got)
	}
}
