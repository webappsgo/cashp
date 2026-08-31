package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Token prefixes per AI.md PART 11. Every prefix includes its trailing
// underscore so a prefix constant can be concatenated directly with the
// random component.
const (
	// PrefixAdmin marks a server admin API token.
	PrefixAdmin = "adm_"
	// PrefixUser marks a regular user API token (multi-user mode).
	PrefixUser = "usr_"
	// PrefixOrg marks an organization API token (orgs mode).
	PrefixOrg = "org_"
	// PrefixAdminAgent marks a server infrastructure agent token.
	PrefixAdminAgent = "adm_agt_"
	// PrefixUserAgent marks a user's personal agent token.
	PrefixUserAgent = "usr_agt_"
	// PrefixOrgAgent marks an organization agent token.
	PrefixOrgAgent = "org_agt_"
)

// TokenRandomLen is the number of alphanumeric characters following a
// token prefix.
const TokenRandomLen = 32

// TokenDisplayLen is the number of leading characters of a token stored
// for display purposes (for example "adm_a1b2").
const TokenDisplayLen = 8

// tokenAlphabet is the alphanumeric set token bodies are drawn from.
const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Token errors. Validation failures are deliberately coarse so they cannot
// be used to distinguish an unknown token from a malformed one.
var (
	// ErrUnknownTokenPrefix is returned when a token prefix is not one of the six defined prefixes.
	ErrUnknownTokenPrefix = errors.New("security: unknown token prefix")
	// ErrInvalidTokenFormat is returned when a token does not match {prefix}_{32 alphanumeric}.
	ErrInvalidTokenFormat = errors.New("security: invalid token format")
)

// TokenPrefixes lists every valid token prefix, longest compound prefixes
// first so prefix matching never mistakes "adm_agt_..." for "adm_...".
var TokenPrefixes = []string{
	PrefixAdminAgent,
	PrefixUserAgent,
	PrefixOrgAgent,
	PrefixAdmin,
	PrefixUser,
	PrefixOrg,
}

// GenerateToken produces a new API token with the given prefix. It returns
// the plaintext token, which is shown to the owner exactly once, and its
// SHA-256 hash, which is the only form persisted.
func GenerateToken(prefix string) (plaintext string, hash string, err error) {
	if !IsValidTokenPrefix(prefix) {
		return "", "", ErrUnknownTokenPrefix
	}

	body, err := randomAlphanumeric(TokenRandomLen)
	if err != nil {
		return "", "", err
	}

	plaintext = prefix + body
	return plaintext, HashToken(plaintext), nil
}

// HashToken returns the lowercase hex SHA-256 digest of a token. Tokens
// are high-entropy random strings, so a fast digest is appropriate — only
// human-chosen passwords need Argon2id.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// TokenDisplayPrefix returns the leading characters of a token kept for
// admin-panel display. It never reveals enough material to reconstruct the
// token.
func TokenDisplayPrefix(plaintext string) string {
	if len(plaintext) <= TokenDisplayLen {
		return plaintext
	}
	return plaintext[:TokenDisplayLen]
}

// IsValidTokenPrefix reports whether prefix is one of the defined token
// prefixes.
func IsValidTokenPrefix(prefix string) bool {
	for _, p := range TokenPrefixes {
		if p == prefix {
			return true
		}
	}
	return false
}

// ParseToken splits a token into its prefix and random body, verifying the
// overall shape. It performs no database lookup and grants no access.
func ParseToken(plaintext string) (prefix string, body string, err error) {
	for _, p := range TokenPrefixes {
		if !strings.HasPrefix(plaintext, p) {
			continue
		}
		body = strings.TrimPrefix(plaintext, p)
		if len(body) != TokenRandomLen || !isAlphanumeric(body) {
			return "", "", ErrInvalidTokenFormat
		}
		return p, body, nil
	}
	return "", "", ErrUnknownTokenPrefix
}

// ConstantTimeEqualString compares two strings without leaking their
// contents or relative length through timing. Both operands are digested
// first so the comparison always runs over a fixed 32-byte width.
func ConstantTimeEqualString(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// randomAlphanumeric returns n characters drawn uniformly from
// tokenAlphabet using crypto/rand.
func randomAlphanumeric(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("security: token length must be positive, got %d", n)
	}

	alphabetLen := big.NewInt(int64(len(tokenAlphabet)))
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", fmt.Errorf("security: read random: %w", err)
		}
		out[i] = tokenAlphabet[idx.Int64()]
	}

	return string(out), nil
}

// isAlphanumeric reports whether s contains only ASCII letters and digits.
func isAlphanumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return len(s) > 0
}
