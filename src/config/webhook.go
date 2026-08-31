package config

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Webhook header names. Every outbound notification carries all of them so
// a receiver can authenticate the payload, reject replays, and deduplicate
// retries without any out-of-band coordination.
const (
	WebhookSignatureHeader = "X-Webhook-Signature"
	WebhookTimestampHeader = "X-Webhook-Timestamp"
	WebhookIDHeader        = "X-Webhook-ID"
	WebhookEventHeader     = "X-Webhook-Event"
)

// WebhookSecretSuffix marks the map key that stores a webhook's signing
// secret alongside its URL, so `alerts` pairs with `alerts_secret`.
const WebhookSecretSuffix = "_secret"

// WebhookReplayWindow is how far a delivery's timestamp may be from the
// receiver's clock before it must be rejected as a replay.
const WebhookReplayWindow = 5 * time.Minute

// WebhookSecretSize is the byte length of a generated signing secret.
const WebhookSecretSize = 32

// WebhookRetryBackoff is the delay before each retry of a delivery that
// returned a non-2xx status. After the last entry the delivery is dropped.
var WebhookRetryBackoff = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// WebhookRetryDelay returns how long to wait before attempt number n
// (1-based) and whether another attempt should be made at all.
func WebhookRetryDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 || attempt > len(WebhookRetryBackoff) {
		return 0, false
	}
	return WebhookRetryBackoff[attempt-1], true
}

// SignWebhook returns the X-Webhook-Signature value for a payload: the
// hex-encoded HMAC-SHA256 of the exact bytes that will be transmitted,
// prefixed with the algorithm so the scheme can be rotated later.
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature reports whether signature matches body under
// secret, comparing in constant time so a mismatch leaks no information
// about the correct value.
func VerifyWebhookSignature(secret string, body []byte, signature string) bool {
	expected := SignWebhook(secret, body)
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(signature)))
}

// VerifyWebhookTimestamp reports whether a delivery timestamp is inside the
// replay window around now. Deliveries from the future are rejected on the
// same margin, since a skewed sender is as unreliable as a replayed one.
func VerifyWebhookTimestamp(timestamp, now time.Time) bool {
	delta := now.Sub(timestamp)
	if delta < 0 {
		delta = -delta
	}
	return delta <= WebhookReplayWindow
}

// GenerateWebhookSecret returns a fresh hex-encoded signing secret.
func GenerateWebhookSecret() (string, error) {
	buf := make([]byte, WebhookSecretSize)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// NewWebhookID returns a UUID version 7 (RFC 9562): a 48-bit millisecond
// timestamp followed by random bits. Time ordering keeps a receiver's
// deduplication index compact, and the same ID is reused across retries of
// one delivery so the receiver can treat repeats as idempotent.
//
// webhookIDMu guards the per-millisecond counter so two IDs minted within
// the same millisecond still sort strictly after one another: the random
// prefix is drawn once per millisecond and a monotonic counter fills the
// rest, matching UUIDv7's monotonic counter method (RFC 9562 section 6.2,
// method 1) instead of redrawing fully random bits that could tie or sort
// out of order.
var (
	webhookIDMu      sync.Mutex
	webhookIDLastMS  uint64
	webhookIDPrefix  uint16
	webhookIDCounter uint64
)

func NewWebhookID() (string, error) {
	var buf [16]byte

	ms := uint64(time.Now().UnixMilli())

	webhookIDMu.Lock()
	if ms > webhookIDLastMS {
		var seed [2]byte
		if _, err := rand.Read(seed[:]); err != nil {
			webhookIDMu.Unlock()
			return "", err
		}
		webhookIDLastMS = ms
		webhookIDPrefix = binary.BigEndian.Uint16(seed[:])
		webhookIDCounter = 0
	} else {
		ms = webhookIDLastMS
		webhookIDCounter++
	}
	prefix, counter := webhookIDPrefix, webhookIDCounter
	webhookIDMu.Unlock()

	binary.BigEndian.PutUint16(buf[6:8], prefix)
	binary.BigEndian.PutUint64(buf[8:16], counter)

	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)

	// Version 7 in the high nibble of byte 6, RFC 4122 variant in byte 8.
	buf[6] = (buf[6] & 0x0f) | 0x70
	buf[8] = (buf[8] & 0x3f) | 0x80

	hexed := hex.EncodeToString(buf[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32], nil
}

// WebhookUserAgent returns the User-Agent every outbound webhook sends,
// identifying the product, its version, and where the instance lives.
func WebhookUserAgent(version, appURL string) string {
	if version == "" {
		version = "devel"
	}

	if appURL == "" {
		return InternalName + "/" + version
	}

	return InternalName + "/" + version + " (+" + appURL + ")"
}

// WebhookDelivery is one signed, ready-to-send notification. The same
// value is reused for every retry: re-signing would change the ID and
// defeat receiver-side deduplication.
type WebhookDelivery struct {
	// Name is the configured webhook this delivery targets.
	Name string
	// URL is the endpoint to POST to.
	URL string
	// Event is the event type, such as "security.breach_detected".
	Event string
	// ID is the UUID v7 shared by every attempt of this delivery.
	ID string
	// Timestamp is when the delivery was created.
	Timestamp time.Time
	// Body is the exact payload the signature covers.
	Body []byte
	// Signature is the X-Webhook-Signature value for Body.
	Signature string
	// UserAgent identifies this instance to the receiver.
	UserAgent string
}

// Headers returns the complete header set for an attempt at this delivery,
// including the content type. The map is rebuilt per call so a caller
// cannot mutate the delivery through it.
func (d WebhookDelivery) Headers() map[string]string {
	return map[string]string{
		"Content-Type":         "application/json",
		"User-Agent":           d.UserAgent,
		WebhookSignatureHeader: d.Signature,
		WebhookTimestampHeader: strconv.FormatInt(d.Timestamp.Unix(), 10),
		WebhookIDHeader:        d.ID,
		WebhookEventHeader:     d.Event,
	}
}

// NewWebhookDelivery signs body for one configured webhook and returns the
// delivery a transport can send and re-send. Sending, status handling, and
// the retry schedule itself belong to the notification layer; everything
// that must stay identical across attempts is fixed here.
func NewWebhookDelivery(name, url, event string, body []byte, secret, userAgent string) (WebhookDelivery, error) {
	if url == "" {
		return WebhookDelivery{}, fmt.Errorf("config: webhook %q has no URL", name)
	}
	if secret == "" {
		return WebhookDelivery{}, fmt.Errorf("config: webhook %q has no signing secret", name)
	}

	id, err := NewWebhookID()
	if err != nil {
		return WebhookDelivery{}, err
	}

	return WebhookDelivery{
		Name:      name,
		URL:       url,
		Event:     event,
		ID:        id,
		Timestamp: time.Now(),
		Body:      body,
		Signature: SignWebhook(secret, body),
		UserAgent: userAgent,
	}, nil
}

// WebhookNames returns the configured webhook names for this contact role,
// sorted for stable iteration and excluding the paired secret entries.
func (r ContactRole) WebhookNames() []string {
	var names []string
	for key, url := range r.Webhooks {
		if strings.HasSuffix(key, WebhookSecretSuffix) || url == "" {
			continue
		}
		names = append(names, key)
	}

	sort.Strings(names)
	return names
}

// WebhookURL returns the endpoint configured under name.
func (r ContactRole) WebhookURL(name string) string {
	return r.Webhooks[name]
}

// EnsureWebhookSecret returns the signing secret for name, generating and
// storing one on first use so an operator only ever has to supply the URL.
func (r *ContactRole) EnsureWebhookSecret(name string) (string, error) {
	if r.Webhooks == nil {
		r.Webhooks = map[string]string{}
	}

	key := name + WebhookSecretSuffix
	if secret := r.Webhooks[key]; secret != "" {
		return secret, nil
	}

	secret, err := GenerateWebhookSecret()
	if err != nil {
		return "", err
	}

	r.Webhooks[key] = secret
	return secret, nil
}

// ContactRoleName identifies one of the four contact roles.
type ContactRoleName string

// The contact roles defined by the spec. Each has its own address and
// webhook set, with a documented fallback chain when it is unset.
const (
	RoleAdmin    ContactRoleName = "admin"
	RoleSecurity ContactRoleName = "security"
	RoleAbuse    ContactRoleName = "abuse"
	RoleGeneral  ContactRoleName = "general"
)

// Role returns the configured role, or nil for an unknown name.
func (c *ContactConfig) Role(name ContactRoleName) *ContactRole {
	switch name {
	case RoleAdmin:
		return &c.Admin
	case RoleSecurity:
		return &c.Security
	case RoleAbuse:
		return &c.Abuse
	case RoleGeneral:
		return &c.General
	default:
		return nil
	}
}

// roleFallbacks is the lookup order for each role. Admin is the universal
// last resort; abuse falls through general first. No chain ever invents an
// address, so an unconfigured abuse contact stays absent rather than
// becoming a guessed abuse@ mailbox.
var roleFallbacks = map[ContactRoleName][]ContactRoleName{
	RoleAdmin:    {RoleAdmin},
	RoleSecurity: {RoleSecurity, RoleAdmin},
	RoleGeneral:  {RoleGeneral, RoleAdmin},
	RoleAbuse:    {RoleAbuse, RoleGeneral, RoleAdmin},
}

// ResolveEmail returns the address to use for a role, following that role's
// fallback chain. It returns an empty string when nothing is configured.
func (c *ContactConfig) ResolveEmail(name ContactRoleName) string {
	for _, candidate := range roleFallbacks[name] {
		if role := c.Role(candidate); role != nil && role.Email != "" {
			return role.Email
		}
	}

	return ""
}

// ExpandFQDN substitutes the {fqdn} placeholder used by the contact
// defaults, so "admin@{fqdn}" becomes a real address once the request's
// hostname is known.
func ExpandFQDN(value, fqdn string) string {
	return strings.ReplaceAll(value, "{fqdn}", fqdn)
}

// EmailFor is ResolveEmail with the {fqdn} placeholder expanded.
func (c *ContactConfig) EmailFor(name ContactRoleName, fqdn string) string {
	return ExpandFQDN(c.ResolveEmail(name), fqdn)
}

// ResolveWebhooks returns the name-to-URL webhook set for a role, following
// the same fallback chain as ResolveEmail so a notification that has no
// role-specific destination still reaches the admin.
func (c *ContactConfig) ResolveWebhooks(name ContactRoleName) map[string]string {
	for _, candidate := range roleFallbacks[name] {
		role := c.Role(candidate)
		if role == nil {
			continue
		}

		names := role.WebhookNames()
		if len(names) == 0 {
			continue
		}

		targets := make(map[string]string, len(names))
		for _, hook := range names {
			targets[hook] = role.Webhooks[hook]
		}
		return targets
	}

	return nil
}
