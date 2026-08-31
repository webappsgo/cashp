package admin

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

// TOTP parameters. These are the values every authenticator app assumes by
// default, so a scanned account works without manual tweaking.
const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew is how many periods either side of now are accepted, which
	// tolerates modest clock drift on the authenticator.
	totpSkew = 1
)

// recoveryCodeCount is how many one-time recovery codes are issued with TOTP.
const recoveryCodeCount = 10

// base32NoPad is the unpadded base32 alphabet TOTP secrets are shared in.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh 160-bit secret in base32, the format
// authenticator apps accept for manual entry.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generate totp secret: %w", err)
	}
	return base32NoPad.EncodeToString(buf), nil
}

// TOTPCode computes the code for a secret at a point in time.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", fmt.Errorf("admin: decode totp secret: %w", err)
	}

	counter := uint64(at.Unix() / int64(totpPeriod.Seconds()))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// VerifyTOTP reports whether a submitted code matches the secret, accepting one
// period of drift either side. The comparison is constant time.
func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits || secret == "" {
		return false
	}
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		candidate, err := TOTPCode(secret, now.Add(time.Duration(skew)*totpPeriod))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// TOTPURI builds the otpauth:// provisioning URI an authenticator imports. It
// is rendered as text and as a QR-ready value; the secret never leaves the
// authenticated admin's own page.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprintf("%d", totpDigits))
	query.Set("period", fmt.Sprintf("%d", int(totpPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// recoveryAlphabet excludes characters that are easy to confuse when a code is
// copied off a screen by hand.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// GenerateRecoveryCodes returns fresh one-time recovery codes in the
// "xxxxx-xxxxx" form. Only their hashes are ever stored.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("admin: generate recovery code: %w", err)
		}
		var b strings.Builder
		for j, v := range buf {
			if j == 5 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// normalizeRecoveryCode makes a pasted code comparable with the stored hash by
// lowercasing it and stripping spaces.
func normalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
}
