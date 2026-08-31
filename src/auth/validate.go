package auth

import (
	"errors"
	"regexp"
	"strings"
)

// Validation errors returned by the Validate* helpers. Handlers translate these into
// the API error envelope; the generic anti-enumeration errors live in errors.go.
var (
	ErrUsernameTooShort  = errors.New("username must be at least 2 characters")
	ErrUsernameTooLong   = errors.New("username cannot exceed 39 characters")
	ErrUsernameCharset   = errors.New("username can only contain lowercase letters, numbers, and hyphen")
	ErrUsernameHyphen    = errors.New("username cannot start or end with a hyphen or contain consecutive hyphens")
	ErrSlugLength        = errors.New("slug must be 2-39 characters")
	ErrSlugCharset       = errors.New("slug must be lowercase alphanumeric with hyphens")
	ErrSlugHyphen        = errors.New("slug cannot contain consecutive hyphens")
	ErrEmailInvalid      = errors.New("please enter a valid email address")
	ErrPasswordTooShort  = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong   = errors.New("password cannot exceed 1024 characters")
	ErrPasswordWhitespce = errors.New("password cannot start or end with whitespace")
)

// usernameRegex enforces the GitHub-style rule set from AI.md PART 34 "Username Validation".
var usernameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// slugRegex is the org slug rule set from AI.md PART 35 "Org Slug Validation" (identical to usernames).
var slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// emailLocalRegex restricts the local part to the character set allowed by AI.md PART 34.
var emailLocalRegex = regexp.MustCompile(`^[a-z0-9.+_-]+$`)

// emailDomainRegex requires a resolvable-looking domain with a real TLD.
var emailDomainRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]\.[a-z]{2,}$`)

// EmailDomainBlocklist holds disposable-email domains rejected at registration.
var EmailDomainBlocklist = []string{
	"tempmail.com", "throwaway.email", "guerrillamail.com",
	"mailinator.com", "10minutemail.com", "trashmail.com",
}

// NormalizeName lowercases and trims a username or org slug for storage and comparison.
func NormalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizeEmail lowercases and trims an email address for storage and comparison.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateUsernameFormat checks only the syntactic rules. It deliberately does not
// consult the blocklist or the database so that callers can order the checks such
// that specific format errors surface before the generic availability answer.
func ValidateUsernameFormat(username string) error {
	username = NormalizeName(username)
	if len(username) < 2 {
		return ErrUsernameTooShort
	}
	if len(username) > 39 {
		return ErrUsernameTooLong
	}
	if strings.Contains(username, "--") {
		return ErrUsernameHyphen
	}
	if strings.HasPrefix(username, "-") || strings.HasSuffix(username, "-") {
		return ErrUsernameHyphen
	}
	if !usernameRegex.MatchString(username) {
		return ErrUsernameCharset
	}
	return nil
}

// ValidateSlugFormat checks the syntactic rules for an organization slug.
func ValidateSlugFormat(slug string) error {
	slug = NormalizeName(slug)
	if len(slug) < 2 || len(slug) > 39 {
		return ErrSlugLength
	}
	if strings.Contains(slug, "--") {
		return ErrSlugHyphen
	}
	if !slugRegex.MatchString(slug) {
		return ErrSlugCharset
	}
	return nil
}

// ValidateEmail applies the RFC-derived rules from AI.md PART 34 "Email Validation Rules".
func ValidateEmail(email string) error {
	email = NormalizeEmail(email)
	if len(email) == 0 || len(email) > 254 {
		return ErrEmailInvalid
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ErrEmailInvalid
	}
	local, domain := parts[0], parts[1]
	if len(local) == 0 || len(local) > 64 {
		return ErrEmailInvalid
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") {
		return ErrEmailInvalid
	}
	if strings.Contains(local, "..") {
		return ErrEmailInvalid
	}
	if len(domain) == 0 || len(domain) > 255 {
		return ErrEmailInvalid
	}
	if !emailLocalRegex.MatchString(local) {
		return ErrEmailInvalid
	}
	if !emailDomainRegex.MatchString(domain) {
		return ErrEmailInvalid
	}
	for _, blocked := range EmailDomainBlocklist {
		if domain == blocked {
			return ErrEmailInvalid
		}
	}
	return nil
}

// ValidatePassword enforces the minimum password policy. Length is capped so a very
// large body cannot be used to burn CPU inside the Argon2id KDF.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	if len(password) > 1024 {
		return ErrPasswordTooLong
	}
	if strings.TrimSpace(password) != password {
		return ErrPasswordWhitespce
	}
	return nil
}

// DetectIdentifierType classifies a login identifier as user_id, email, or username,
// per the detection logic in AI.md PART 34 "Login Identifier".
func DetectIdentifierType(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return "username"
	}
	allDigits := true
	for _, r := range input {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "user_id"
	}
	if strings.Contains(input, "@") {
		return "email"
	}
	return "username"
}
