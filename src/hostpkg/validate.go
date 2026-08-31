package hostpkg

import (
	"regexp"
	"strings"
)

// Every value that reaches an argv element passes through this file first.
// The patterns are strict allowlists, so shell metacharacters, whitespace,
// path traversal, and leading dashes (which would be read as flags) are all
// rejected before a command is ever built.

// Length caps for the validated identifiers.
const (
	maxPackageNameLen = 128
	maxRepoNameLen    = 64
	maxVersionLen     = 32
)

var (
	// packageNamePattern accepts the character set every supported package
	// manager uses for package names and nothing else.
	packageNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)
	// repoNamePattern restricts repository identifiers, which also become
	// on-disk definition file names.
	repoNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// phpVersionPattern accepts a two-component PHP version such as "8.3".
	phpVersionPattern = regexp.MustCompile(`^[0-9]\.[0-9]$`)
	// codenamePattern accepts an apt suite name.
	codenamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ValidatePackageName enforces the package-name allowlist.
func ValidatePackageName(name string) error {
	if name == "" || len(name) > maxPackageNameLen {
		return failValidation(ErrInvalidPackageName, "invalid package name")
	}
	if !packageNamePattern.MatchString(name) {
		return failValidation(ErrInvalidPackageName, "invalid package name")
	}
	if strings.Contains(name, "..") {
		return failValidation(ErrInvalidPackageName, "invalid package name")
	}

	return nil
}

// ValidatePackageNames validates a whole set and rejects an empty set, since
// an empty argument list would turn an install into a no-op or, worse, turn
// an upgrade into a whole-system operation.
func ValidatePackageNames(names []string) error {
	if len(names) == 0 {
		return failValidation(ErrNoPackages, "no packages were requested")
	}
	for _, name := range names {
		if err := ValidatePackageName(name); err != nil {
			return err
		}
	}

	return nil
}

// ValidateRepoName enforces the repository-identifier allowlist.
func ValidateRepoName(name string) error {
	if name == "" || len(name) > maxRepoNameLen {
		return failValidation(ErrInvalidRepoName, "invalid repository name")
	}
	if !repoNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return failValidation(ErrInvalidRepoName, "invalid repository name")
	}

	return nil
}

// ValidatePHPVersion enforces the "X.Y" form used for multi-version PHP-FPM.
func ValidatePHPVersion(version string) error {
	if version == "" || len(version) > maxVersionLen || !phpVersionPattern.MatchString(version) {
		return failValidation(ErrInvalidVersion, "invalid PHP version")
	}

	return nil
}

// ValidateCodename enforces the apt suite-name allowlist before a codename
// is written into a repository definition file.
func ValidateCodename(codename string) error {
	if codename == "" || len(codename) > maxVersionLen || !codenamePattern.MatchString(codename) {
		return failValidation(ErrInvalidVersion, "invalid distribution codename")
	}

	return nil
}

// DedupePackages returns the input with duplicates removed, preserving order,
// so an install transaction never lists the same package twice.
func DedupePackages(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}

	return out
}
