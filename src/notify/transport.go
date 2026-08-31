package notify

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// Outbound HTTP limits. Every webhook delivery is bounded so a hostile or
// hung receiver cannot pin a worker or exhaust memory.
const (
	// httpTimeout is the total budget for one webhook delivery.
	httpTimeout = 15 * time.Second
	// dialTimeout is the budget for establishing the TCP connection.
	dialTimeout = 5 * time.Second
	// maxRedirects is how many hops a delivery may follow. Each hop is
	// re-validated against the SSRF guard.
	maxRedirects = 3
	// maxResponseBody is how much of a receiver's response is drained before
	// the connection is returned to the pool.
	maxResponseBody = 4 << 10
	// errorSnippetLen is how much of a failing response is kept as an
	// operator hint in the delivery log.
	errorSnippetLen = 256
)

// ErrBlockedDestination rejects a webhook URL that fails the SSRF guard.
var ErrBlockedDestination = errors.New(errors.CodeValidation, http.StatusBadRequest, "webhook destination is not allowed")

// ErrDeliveryRejected reports a non-2xx response from a webhook receiver.
var ErrDeliveryRejected = errors.New(errors.CodeUnavailable, http.StatusBadGateway, "webhook receiver rejected the delivery")

// httpDoer is the subset of *http.Client the webhook channels use. Tests
// substitute their own implementation.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// newOutboundClient returns an HTTP client hardened for user-supplied
// destinations: bounded timeouts, no connection reuse across hosts beyond
// a small pool, and an SSRF re-check on every redirect hop. Validating only
// the initial URL is not enough, because a receiver can answer with a 302
// pointing at a link-local metadata address.
func newOutboundClient() *http.Client {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	return &http.Client{
		Timeout:   httpTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New(errors.CodeValidation, http.StatusBadRequest, "webhook delivery exceeded the redirect limit")
			}
			if err := security.ValidateOutboundURL(req.URL.String()); err != nil {
				return ErrBlockedDestination.WithDetails(map[string]any{"reason": err.Error()})
			}
			return nil
		},
	}
}

// postJSON delivers a signed JSON payload to a validated destination. The
// destination is checked before a socket is opened and again on every
// redirect by the client's CheckRedirect.
func postJSON(ctx context.Context, client httpDoer, url string, body []byte, headers map[string]string) error {
	if err := security.ValidateOutboundURL(url); err != nil {
		return ErrBlockedDestination.WithDetails(map[string]any{"reason": err.Error()})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "build webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusBadGateway, "webhook delivery failed")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Only a short prefix of the receiver's response is kept, purely as an
	// operator hint in the delivery log. It is never parsed or replayed.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorSnippetLen))
	return ErrDeliveryRejected.WithDetails(map[string]any{
		"status": resp.StatusCode,
		"body":   string(bytes.TrimSpace(snippet)),
	})
}

// RetryableDelivery reports whether a delivery error is worth retrying. A
// blocked destination or a receiver that rejected the payload outright is
// a configuration fault, not a transient one, so it is not retried.
func RetryableDelivery(err error) bool {
	if err == nil {
		return false
	}
	appErr := errors.From(err)
	switch appErr.Code {
	case errors.CodeValidation, errors.CodeNotFound:
		return false
	default:
		return true
	}
}
