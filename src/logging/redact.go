package logging

import (
	"log/slog"
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// neverRedacted lists attribute names that collide with a sensitive
// substring but carry no secret material and are required in every error
// log line.
var neverRedacted = map[string]bool{
	"error_code":  true,
	"status_code": true,
	"token_id":    true,
	"key_id":      true,
}

// redactAttr is the slog ReplaceAttr hook installed on every handler,
// including the audit handler. It enforces the PART 11 rule that
// passwords, full API and session tokens, recovery keys, TOTP secrets, and
// private keys never reach a log file. Values are masked, never dropped,
// so the shape of a log line stays stable.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if !neverRedacted[a.Key] && security.IsSensitiveName(a.Key) {
		return slog.String(a.Key, security.MaskedValue)
	}

	if a.Value.Kind() != slog.KindString {
		return a
	}

	return slog.String(a.Key, redactValue(a.Key, a.Value.String()))
}

// redactValue masks values that are self-evidently secret regardless of
// the attribute name: API tokens, password hashes, and PEM private key
// blocks. URL-valued attributes have their sensitive query parameters
// stripped instead of being masked wholesale.
func redactValue(key, value string) string {
	if value == "" {
		return value
	}

	if _, _, err := security.ParseToken(value); err == nil {
		return security.HashToken(value)
	}

	if strings.HasPrefix(value, "$argon2id$") || strings.HasPrefix(value, "$2a$") ||
		strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$") {
		return security.MaskedValue
	}

	if strings.Contains(value, "PRIVATE KEY-----") {
		return security.MaskedValue
	}

	if isURLKey(key) && (strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")) {
		return security.RedactURL(value)
	}

	return security.StripControlChars(value)
}

// isURLKey reports whether an attribute name denotes a URL, so its query
// string is redacted rather than the whole value masked.
func isURLKey(key string) bool {
	lower := strings.ToLower(key)
	return lower == "url" || lower == "uri" || lower == "referer" || lower == "referrer" ||
		strings.HasSuffix(lower, "_url") || strings.HasSuffix(lower, "_uri")
}
