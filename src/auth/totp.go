package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// totpPeriod is the RFC 6238 time step in seconds.
const totpPeriod int64 = 30

// totpDigits is the number of digits in a generated code.
const totpDigits = 6

// totpSkew is how many time steps before and after "now" are accepted, absorbing clock drift.
const totpSkew int64 = 1

// totpEncoding is unpadded base32 as used by every authenticator app.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret generates a fresh 20-byte shared secret encoded as unpadded base32.
// The plaintext secret is returned to the caller once; it must be stored encrypted.
func NewTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return totpEncoding.EncodeToString(buf), nil
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator app scans.
// The secret is embedded in the URI, so callers must never log the result.
func TOTPProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpAt computes the code for a given counter value.
func totpAt(secret string, counter int64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("decode totp secret: %w", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// TOTPCode returns the code valid at time t for the given secret.
func TOTPCode(secret string, t time.Time) (string, error) {
	return totpAt(secret, t.Unix()/totpPeriod)
}

// ValidateTOTP reports whether code matches the secret within the accepted skew window.
// Every candidate is compared in constant time and the loop always runs to completion,
// so a match late in the window costs the same as a match at the start.
func ValidateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits || secret == "" {
		return false
	}
	counter := time.Now().Unix() / totpPeriod
	matched := 0
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		candidate, err := totpAt(secret, counter+delta)
		if err != nil {
			return false
		}
		matched |= subtle.ConstantTimeCompare([]byte(candidate), []byte(code))
	}
	return matched == 1
}
