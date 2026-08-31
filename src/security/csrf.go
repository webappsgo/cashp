package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// CSRFTokenTTL is how long an issued CSRF token stays valid. Tokens are
// self-contained HMACs, so no server-side storage is required.
const CSRFTokenTTL = 12 * time.Hour

// csrfClockSkew is the tolerance applied to tokens whose issue time is in
// the future, covering small clock differences between cluster nodes.
const csrfClockSkew = 2 * time.Minute

// csrfNonceLen is the length in bytes of the random component mixed into
// every CSRF token so two tokens for the same session never collide.
const csrfNonceLen = 16

// csrfNow is the clock used when minting and validating tokens. Tests
// replace it to exercise expiry deterministically.
var csrfNow = time.Now

// NewCSRFToken mints a CSRF token bound to sessionID and keyed by secret,
// which is the csrf_token_secret from app_secrets. The returned value is
// "{issuedUnix}.{nonce}.{mac}" and is safe to place in a form field or a
// double-submit cookie.
func NewCSRFToken(secret []byte, sessionID string) string {
	nonceRaw := make([]byte, csrfNonceLen)
	if _, err := rand.Read(nonceRaw); err != nil {
		return ""
	}

	issued := strconv.FormatInt(csrfNow().Unix(), 10)
	nonce := base64.RawURLEncoding.EncodeToString(nonceRaw)

	return issued + "." + nonce + "." + csrfMAC(secret, sessionID, issued, nonce)
}

// ValidateCSRFToken reports whether token is a well-formed, unexpired
// token minted by NewCSRFToken for the same secret and sessionID. The MAC
// is compared in constant time.
func ValidateCSRFToken(secret []byte, sessionID, token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}

	issued, nonce, mac := parts[0], parts[1], parts[2]
	if nonce == "" || mac == "" {
		return false
	}

	issuedUnix, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}

	if !hmac.Equal([]byte(csrfMAC(secret, sessionID, issued, nonce)), []byte(mac)) {
		return false
	}

	age := csrfNow().Sub(time.Unix(issuedUnix, 0))
	if age > CSRFTokenTTL || age < -csrfClockSkew {
		return false
	}

	return true
}

// csrfMAC computes the HMAC-SHA256 over the session binding and token
// metadata, encoded for transport.
func csrfMAC(secret []byte, sessionID, issued, nonce string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(sessionID))
	m.Write([]byte("\x00"))
	m.Write([]byte(issued))
	m.Write([]byte("\x00"))
	m.Write([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
