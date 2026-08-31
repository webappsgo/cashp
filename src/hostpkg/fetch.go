package hostpkg

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// Signing keys are small; anything larger is either the wrong URL or an
// attempt to exhaust memory, so the body is hard-capped.
const (
	maxKeyBytes    = 1 << 20
	fetchTimeout   = 30 * time.Second
	fetchUserAgent = "cashp"
)

// Fetcher retrieves a remote artifact. The only artifact cashp ever fetches
// is a repository signing key, and it is verified against a pinned
// fingerprint before it is trusted, so the transport is not the trust anchor.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) ([]byte, error)
}

// HTTPFetcher is the production Fetcher. It is HTTPS-only, runs every URL
// through security.ValidateOutboundURL, refuses redirects to a different
// host, and never executes anything it downloads.
type HTTPFetcher struct {
	// Client performs the request; a nil client uses a bounded default.
	Client *http.Client
	// MaxBytes caps the response body.
	MaxBytes int64
}

// NewHTTPFetcher returns a fetcher with bounded timeouts and no redirect
// following, so a compromised mirror cannot bounce the request elsewhere.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		Client: &http.Client{
			Timeout: fetchTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 {
					return nil
				}
				if len(via) > 5 {
					return http.ErrUseLastResponse
				}
				if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		MaxBytes: maxKeyBytes,
	}
}

// Fetch downloads a signing key over HTTPS.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if err := ValidateKeyURL(rawURL); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, failValidation(ErrInsecureRepoURL, "repository signing key location is not valid")
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", "*/*")

	client := f.Client
	if client == nil {
		client = NewHTTPFetcher().Client
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, failUnavailable(ErrCommandFailed, "repository signing key could not be downloaded")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, failUnavailable(ErrCommandFailed, "repository signing key could not be downloaded").
			WithDetails(map[string]any{"status": resp.StatusCode})
	}

	limit := f.MaxBytes
	if limit <= 0 {
		limit = maxKeyBytes
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, failUnavailable(ErrCommandFailed, "repository signing key could not be downloaded")
	}
	if int64(len(body)) > limit {
		return nil, failValidation(ErrKeyTooLarge, "repository signing key is larger than expected")
	}

	return body, nil
}

// ValidateKeyURL enforces the outbound rules for a repository signing key:
// HTTPS only, no embedded credentials, and the shared outbound guard that
// blocks loopback, link-local and private destinations.
func ValidateKeyURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return failValidation(ErrInsecureRepoURL, "repository signing key location is not valid")
	}
	if parsed.Scheme != "https" {
		return failValidation(ErrInsecureRepoURL, "repository signing key must be served over HTTPS")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" {
		return failValidation(ErrInsecureRepoURL, "repository signing key location is not valid")
	}
	if err := security.ValidateOutboundURL(raw); err != nil {
		return failValidation(ErrInsecureRepoURL, "repository signing key location is not allowed")
	}

	return nil
}

// StaticFetcher serves key material from memory. It backs the test suite and
// any deployment that ships its keys on disk instead of fetching them.
type StaticFetcher struct {
	// Keys maps a URL to its exact key bytes.
	Keys map[string][]byte
	// Requests records every requested URL in call order.
	Requests []string
}

// NewStaticFetcher returns an empty in-memory fetcher.
func NewStaticFetcher() *StaticFetcher {
	return &StaticFetcher{Keys: map[string][]byte{}}
}

// Set registers the bytes served for a URL.
func (f *StaticFetcher) Set(rawURL string, data []byte) {
	if f.Keys == nil {
		f.Keys = map[string][]byte{}
	}
	f.Keys[rawURL] = data
}

// Fetch returns the registered bytes for a URL.
func (f *StaticFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	f.Requests = append(f.Requests, rawURL)

	data, ok := f.Keys[rawURL]
	if !ok {
		return nil, failUnavailable(ErrCommandFailed, "repository signing key could not be downloaded")
	}

	return data, nil
}
