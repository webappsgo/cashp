package admin

import (
	"fmt"
	"strings"
)

// DefaultAdminPath is the path segment used when the operator does not choose
// one. It is deliberately not "admin": the panel location is configurable and
// nothing public ever links to it.
const DefaultAdminPath = "administration"

// adminRoot is the fixed prefix every server-side route lives under.
const adminRoot = "/server/"

// reservedPaths are segments already owned by the public site or by the
// platform. An admin path may never collide with one of them.
var reservedPaths = map[string]bool{
	"api":         true,
	"health":      true,
	"healthz":     true,
	"metrics":     true,
	"version":     true,
	".well-known": true,
	"about":       true,
	"privacy":     true,
	"contact":     true,
	"help":        true,
	"terms":       true,
	"preferences": true,
	"docs":        true,
	"auth":        true,
	"security":    true,
	"static":      true,
	"assets":      true,
}

// NormalizeAdminPath trims decoration from a configured admin path and
// validates it. An empty value yields the default.
func NormalizeAdminPath(value string) (string, error) {
	trimmed := strings.ToLower(strings.Trim(strings.TrimSpace(value), "/"))
	if trimmed == "" {
		trimmed = DefaultAdminPath
	}
	if err := ValidateAdminPath(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

// ValidateAdminPath enforces the segment rules: lowercase letters, digits and
// hyphens only, 2-32 characters, no leading or trailing hyphen, and never a
// reserved segment.
func ValidateAdminPath(value string) error {
	if len(value) < 2 || len(value) > 32 {
		return fmt.Errorf("admin: path must be between 2 and 32 characters")
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return fmt.Errorf("admin: path must not start or end with a hyphen")
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("admin: path may only contain lowercase letters, digits and hyphens")
		}
	}
	if reservedPaths[value] {
		return fmt.Errorf("admin: path %q is reserved", value)
	}
	return nil
}

// ValidateUsername enforces the admin username rules. Usernames become a URL
// segment, so they follow the same character set as the admin path.
func ValidateUsername(value string) error {
	if len(value) < 2 || len(value) > 32 {
		return fmt.Errorf("username must be between 2 and 32 characters")
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return fmt.Errorf("username must not start or end with a hyphen")
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		case r == '_':
		default:
			return fmt.Errorf("username may only contain lowercase letters, digits, hyphens and underscores")
		}
	}
	if value == "config" {
		return fmt.Errorf("username %q is reserved", value)
	}
	return nil
}

// base returns the absolute URL prefix of the panel, without a trailing slash.
func (p *Panel) base() string {
	return adminRoot + p.adminPath
}

// url joins the panel base with a relative path. Callers pass paths without a
// leading slash, e.g. "config/settings".
func (p *Panel) url(rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return p.base()
	}
	return p.base() + "/" + rel
}
