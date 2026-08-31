package guard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"strings"
	"sync"
	"time"
)

// SignatureAlgorithm names the MAC a payment or DNS provider signs its
// webhooks with. The set is closed: an algorithm the provider integration
// does not name cannot be selected by anything in the request, which is
// what stops an attacker downgrading a verification to "none".
type SignatureAlgorithm string

// The supported webhook signature algorithms.
const (
	// SignatureHMACSHA256 is HMAC-SHA256, what most payment providers use.
	SignatureHMACSHA256 SignatureAlgorithm = "hmac-sha256"
	// SignatureHMACSHA512 is HMAC-SHA512.
	SignatureHMACSHA512 SignatureAlgorithm = "hmac-sha512"
)

// SignatureEncoding is how the provider renders the MAC in the header.
type SignatureEncoding string

// The supported signature encodings.
const (
	// EncodingHex is lowercase or uppercase hexadecimal.
	EncodingHex SignatureEncoding = "hex"
	// EncodingBase64 is standard base64.
	EncodingBase64 SignatureEncoding = "base64"
)

// DefaultWebhookTolerance is how far a webhook timestamp may sit from the
// receiver's clock before the delivery is refused. It is wide enough for
// ordinary clock drift and provider retry latency, and narrow enough that a
// captured delivery cannot be replayed later in the day.
const DefaultWebhookTolerance = 5 * time.Minute

// WebhookPolicy describes how one provider's webhooks are verified.
type WebhookPolicy struct {
	// Algorithm is the MAC the provider signs with.
	Algorithm SignatureAlgorithm
	// Encoding is how the MAC is rendered in the signature header.
	Encoding SignatureEncoding
	// Tolerance is the accepted clock skew; zero uses DefaultWebhookTolerance.
	Tolerance time.Duration
}

// VerifyWebhook checks a webhook delivery's authenticity. It refuses the
// delivery unless the signature verifies in constant time against the
// shared secret and the signed timestamp is inside the tolerance window, so
// neither a forged event nor a captured-and-replayed one is accepted.
//
// signedPayload must be assembled exactly as the provider specifies —
// typically the timestamp joined to the raw body — and must be built from
// the raw bytes read off the wire, never from a re-serialized struct: a
// re-serialization changes the bytes and would force the verification to be
// loosened to compensate.
func VerifyWebhook(policy WebhookPolicy, secret Secret, signedPayload []byte, providedSignature string, timestamp, now time.Time) error {
	if secret.Empty() {
		return Deny(ReasonSignatureInvalid, "webhook secret is not configured")
	}
	if len(signedPayload) == 0 {
		return Deny(ReasonSignatureInvalid, "webhook payload is empty")
	}
	providedSignature = strings.TrimSpace(providedSignature)
	if providedSignature == "" {
		return Deny(ReasonSignatureInvalid, "webhook carries no signature")
	}

	tolerance := policy.Tolerance
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	if timestamp.IsZero() {
		return Deny(ReasonReplay, "webhook carries no timestamp")
	}
	drift := now.Sub(timestamp)
	if drift < 0 {
		drift = -drift
	}
	if drift > tolerance {
		return Deny(ReasonReplay, "webhook timestamp is outside the tolerance window")
	}

	expected, err := signPayload(policy.Algorithm, secret.Reveal(), signedPayload)
	if err != nil {
		return err
	}
	provided, err := decodeSignature(policy.Encoding, providedSignature)
	if err != nil {
		return err
	}
	// hmac.Equal is constant time and safe on differing lengths, so a
	// truncated signature leaks nothing through timing either.
	if !hmac.Equal(expected, provided) {
		return Deny(ReasonSignatureInvalid, "webhook signature did not verify")
	}
	return nil
}

// SignPayload produces the MAC for a payload under a policy. It exists so
// an outbound webhook cashp itself sends is signed by the same code path
// that verifies an inbound one, rather than by a second implementation that
// could drift out of agreement with it.
func SignPayload(policy WebhookPolicy, secret Secret, payload []byte) (string, error) {
	mac, err := signPayload(policy.Algorithm, secret.Reveal(), payload)
	if err != nil {
		return "", err
	}
	switch policy.Encoding {
	case EncodingBase64:
		return base64.StdEncoding.EncodeToString(mac), nil
	case EncodingHex, "":
		return hex.EncodeToString(mac), nil
	default:
		return "", Deny(ReasonSignatureInvalid, "signature encoding "+string(policy.Encoding)+" is not supported")
	}
}

// signPayload computes the MAC for a payload under the named algorithm. An
// unnamed or unrecognized algorithm is an error rather than a default, so a
// zero-valued policy cannot silently verify nothing.
func signPayload(algorithm SignatureAlgorithm, secret string, payload []byte) ([]byte, error) {
	var newHash func() hash.Hash
	switch algorithm {
	case SignatureHMACSHA256:
		newHash = sha256.New
	case SignatureHMACSHA512:
		newHash = sha512.New
	default:
		return nil, Deny(ReasonSignatureInvalid, "signature algorithm "+string(algorithm)+" is not supported")
	}
	mac := hmac.New(newHash, []byte(secret))
	mac.Write(payload)
	return mac.Sum(nil), nil
}

// decodeSignature decodes a provided signature, tolerating a provider's
// choice of case in hex but nothing else. A signature that does not decode
// is a refusal, never an empty comparison that would trivially match.
func decodeSignature(encoding SignatureEncoding, provided string) ([]byte, error) {
	switch encoding {
	case EncodingBase64:
		raw, err := base64.StdEncoding.DecodeString(provided)
		if err != nil {
			return nil, Deny(ReasonSignatureInvalid, "webhook signature is not valid base64")
		}
		if len(raw) == 0 {
			return nil, Deny(ReasonSignatureInvalid, "webhook signature decoded to nothing")
		}
		return raw, nil
	case EncodingHex, "":
		raw, err := hex.DecodeString(strings.ToLower(provided))
		if err != nil {
			return nil, Deny(ReasonSignatureInvalid, "webhook signature is not valid hex")
		}
		if len(raw) == 0 {
			return nil, Deny(ReasonSignatureInvalid, "webhook signature decoded to nothing")
		}
		return raw, nil
	default:
		return nil, Deny(ReasonSignatureInvalid, "signature encoding "+string(encoding)+" is not supported")
	}
}

// ReplayGuard records provider event identifiers that have already been
// processed, so a delivery replayed inside the signature tolerance window
// is refused the second time. Retention must exceed the tolerance window,
// or a replay could be accepted after its record expired.
//
// It is an in-memory guard for a single process; a clustered deployment
// must additionally enforce uniqueness on the event identifier in storage,
// which is the durable half of the same protection.
type ReplayGuard struct {
	mu        sync.Mutex
	retention time.Duration
	seen      map[string]time.Time
	nowFunc   func() time.Time
}

// NewReplayGuard creates a guard retaining event identifiers for the given
// duration. A retention of zero or less uses twice DefaultWebhookTolerance,
// which is the shortest span that cannot expire a record while the
// signature on that record is still inside its own window.
func NewReplayGuard(retention time.Duration) *ReplayGuard {
	if retention <= 0 {
		retention = 2 * DefaultWebhookTolerance
	}
	return &ReplayGuard{
		retention: retention,
		seen:      make(map[string]time.Time),
		nowFunc:   time.Now,
	}
}

// SetClock replaces the guard's clock. A nil clock is ignored.
func (g *ReplayGuard) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nowFunc = now
}

// Claim records an event identifier and reports a denial if it was already
// recorded. It must be called only after the signature verified: claiming
// first would let an unauthenticated caller poison the guard with the
// identifiers of deliveries that had not happened yet.
//
// A delivery with no identifier is refused rather than admitted, because an
// unidentified delivery cannot be deduplicated at all.
func (g *ReplayGuard) Claim(provider, eventID string) error {
	if provider == "" || eventID == "" {
		return Deny(ReasonReplay, "webhook delivery has no provider or event identifier")
	}
	key := provider + "\x00" + eventID

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.nowFunc()
	cutoff := now.Add(-g.retention)
	for existing, at := range g.seen {
		if at.Before(cutoff) {
			delete(g.seen, existing)
		}
	}
	if _, replayed := g.seen[key]; replayed {
		return Deny(ReasonReplay, "webhook event "+eventID+" from "+provider+" was already processed")
	}
	g.seen[key] = now
	return nil
}

// Cleanup drops event identifiers older than the retention window.
func (g *ReplayGuard) Cleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()

	cutoff := g.nowFunc().Add(-g.retention)
	for key, at := range g.seen {
		if at.Before(cutoff) {
			delete(g.seen, key)
		}
	}
}
