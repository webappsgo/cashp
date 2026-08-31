package hostpkg

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidateKeyURLRejectsUnsafeLocations(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"plain http", "http://download.docker.com/linux/debian/gpg"},
		{"no scheme", "download.docker.com/linux/debian/gpg"},
		{"file scheme", "file:///etc/shadow"},
		{"embedded credentials", "https://user:token@download.docker.com/linux/debian/gpg"},
		{"fragment", "https://download.docker.com/linux/debian/gpg#frag"},
		{"no host", "https:///linux/debian/gpg"},
		{"loopback literal", "https://127.0.0.1/gpg"},
		{"localhost", "https://localhost/gpg"},
		{"link local metadata", "https://169.254.169.254/latest/meta-data/"},
		{"private range", "https://10.0.0.5/gpg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateKeyURL(tc.raw); !errors.Is(err, ErrInsecureRepoURL) {
				t.Fatalf("ValidateKeyURL(%q) = %v, want ErrInsecureRepoURL", tc.raw, err)
			}
		})
	}
}

func TestValidateKeyURLAcceptsPublicHTTPS(t *testing.T) {
	// A public IP literal is used so the check never depends on DNS being
	// reachable from the test environment.
	if err := ValidateKeyURL("https://93.184.216.34/linux/debian/gpg"); err != nil {
		t.Fatalf("ValidateKeyURL = %v, want nil", err)
	}
}

func TestPinnedKeyURLsAreHTTPSAndFragmentFree(t *testing.T) {
	for _, id := range Repos() {
		for family := range distroFixtures {
			plan, err := PlanRepo(id, distroFor(t, family))
			if err != nil {
				continue
			}
			if len(plan.Keys) == 0 {
				t.Fatalf("repo %s on %s has no pinned signing key", id, family)
			}
			for _, key := range plan.Keys {
				if !strings.HasPrefix(key.URL, "https://") {
					t.Errorf("repo %s key %s is not fetched over HTTPS: %s", id, key.Fingerprint, key.URL)
				}
				if strings.ContainsAny(key.URL, "#@") {
					t.Errorf("repo %s key URL is not a plain location: %s", id, key.URL)
				}
				if _, err := NormalizeFingerprint(key.Fingerprint); err != nil {
					t.Errorf("repo %s has an unusable pinned fingerprint %q: %v", id, key.Fingerprint, err)
				}
			}
		}
	}
}

// failingTransport fails the test if the fetcher ever opens a connection.
type failingTransport struct {
	t *testing.T
}

// RoundTrip records an unexpected outbound request.
func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Fatalf("an outbound request was attempted: %s", req.URL.Redacted())

	return nil, errors.New("unreachable")
}

func TestHTTPFetcherRefusesInsecureURLBeforeAnyRequest(t *testing.T) {
	fetcher := NewHTTPFetcher()
	if fetcher.Client == nil || fetcher.MaxBytes != maxKeyBytes {
		t.Fatalf("NewHTTPFetcher returned an unbounded client: %+v", fetcher)
	}

	// The URL check must reject these before a connection is ever opened.
	fetcher.Client.Transport = failingTransport{t: t}
	for _, raw := range []string{"http://example.com/gpg", "https://127.0.0.1/gpg"} {
		if _, err := fetcher.Fetch(t.Context(), raw); !errors.Is(err, ErrInsecureRepoURL) {
			t.Errorf("Fetch(%q) = %v, want ErrInsecureRepoURL", raw, err)
		}
	}
}

func TestStaticFetcher(t *testing.T) {
	fetcher := NewStaticFetcher()
	fetcher.Set("https://download.docker.com/linux/debian/gpg", []byte("key-bytes"))

	data, err := fetcher.Fetch(t.Context(), "https://download.docker.com/linux/debian/gpg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "key-bytes" {
		t.Fatalf("Fetch = %q", data)
	}

	if _, err := fetcher.Fetch(t.Context(), "https://download.docker.com/missing"); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("missing key error = %v, want ErrCommandFailed", err)
	}

	want := []string{
		"https://download.docker.com/linux/debian/gpg",
		"https://download.docker.com/missing",
	}
	if strings.Join(fetcher.Requests, ",") != strings.Join(want, ",") {
		t.Fatalf("Requests = %v, want %v", fetcher.Requests, want)
	}
}
