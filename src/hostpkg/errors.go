package hostpkg

import (
	"errors"
	"net/http"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Sentinel causes for every failure mode this package can produce. They are
// plain errors so callers can match them with errors.Is even after the
// typed wrapper has been copied by WithDetails, while the wrapper carries
// the API-visible code, status, and message.
var (
	// ErrNotLinux is returned when cashp runs on a non-Linux host, where no
	// host package management exists. This is a hard error, not a fallback.
	ErrNotLinux = errors.New("hostpkg: host operating system is not linux")
	// ErrOSReleaseMissing is returned when neither os-release file is readable.
	ErrOSReleaseMissing = errors.New("hostpkg: os-release not found")
	// ErrOSReleaseMalformed is returned when os-release cannot be parsed.
	ErrOSReleaseMalformed = errors.New("hostpkg: os-release is malformed")
	// ErrUnsupportedDistro is returned for a distribution outside the set
	// IDEA.md lists as supported.
	ErrUnsupportedDistro = errors.New("hostpkg: distribution is not supported")
	// ErrVersionTooOld is returned when the distribution is supported but the
	// running release is below the documented floor.
	ErrVersionTooOld = errors.New("hostpkg: distribution release is below the supported floor")
	// ErrInvalidPackageName is returned when a package name fails the strict
	// allowlist that guards every argv element.
	ErrInvalidPackageName = errors.New("hostpkg: invalid package name")
	// ErrInvalidRepoName is returned when a repository identifier fails the
	// allowlist used for on-disk definition file names.
	ErrInvalidRepoName = errors.New("hostpkg: invalid repository name")
	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("hostpkg: invalid version")
	// ErrInvalidCommand is returned when a command name fails validation
	// before it can be handed to the process runner.
	ErrInvalidCommand = errors.New("hostpkg: invalid command")
	// ErrNoPackages is returned when an operation is called with no packages.
	ErrNoPackages = errors.New("hostpkg: no packages given")
	// ErrCommandFailed is returned when a package manager exits non-zero.
	ErrCommandFailed = errors.New("hostpkg: package manager command failed")
	// ErrCommandTimeout is returned when a package manager had to be killed.
	ErrCommandTimeout = errors.New("hostpkg: package manager command timed out")
	// ErrServiceUnknown is returned for a service outside the managed set.
	ErrServiceUnknown = errors.New("hostpkg: unknown managed service")
	// ErrServiceNotAvailable is returned where IDEA.md's mapping table shows
	// no package for the running distribution.
	ErrServiceNotAvailable = errors.New("hostpkg: service is not available on this distribution")
	// ErrHoldUnsupported is returned by managers with no version-hold concept.
	ErrHoldUnsupported = errors.New("hostpkg: package hold is not supported by this package manager")
	// ErrRepoUnknown is returned for an unknown third-party repository id.
	ErrRepoUnknown = errors.New("hostpkg: unknown repository")
	// ErrRepoNotApplicable is returned when the running distribution needs no
	// third-party repository for the requested feature.
	ErrRepoNotApplicable = errors.New("hostpkg: repository is not applicable to this distribution")
	// ErrInsecureRepoURL is returned when a repository or key URL is not
	// https or fails the outbound SSRF guard.
	ErrInsecureRepoURL = errors.New("hostpkg: repository url is not permitted")
	// ErrKeyUnparsable is returned when fetched key material is not a
	// readable OpenPGP public key.
	ErrKeyUnparsable = errors.New("hostpkg: signing key could not be parsed")
	// ErrKeyFingerprintMismatch is returned when a fetched signing key does
	// not match its pinned fingerprint. Nothing is written when this occurs.
	ErrKeyFingerprintMismatch = errors.New("hostpkg: signing key fingerprint does not match the pinned value")
	// ErrKeyTooLarge is returned when a key download exceeds the size cap.
	ErrKeyTooLarge = errors.New("hostpkg: signing key download exceeds the size limit")
	// ErrNotOwned is returned when a removal targets a package cashp did not
	// install, which cashp refuses to touch.
	ErrNotOwned = errors.New("hostpkg: package was not installed by cashp")
	// ErrDistroUpgradeRefused is returned when a caller asks for a whole-system
	// distribution upgrade, which cashp never performs.
	ErrDistroUpgradeRefused = errors.New("hostpkg: distribution upgrade is never performed")
	// ErrPathEscape is returned when a target path escapes the filesystem root.
	ErrPathEscape = errors.New("hostpkg: path escapes the filesystem root")
)

// fail wraps a sentinel cause in the typed application error carrying the
// API-visible code, status, and message. The message never contains a
// command line, a token, or a filesystem path.
func fail(cause error, code string, status int, message string) *apperr.Error {
	return apperr.Wrap(cause, code, status, message)
}

// failValidation is the shorthand for the input-validation failures that
// guard argv construction.
func failValidation(cause error, message string) *apperr.Error {
	return fail(cause, apperr.CodeValidation, http.StatusBadRequest, message)
}

// failUnavailable is the shorthand for host-state failures such as an
// unsupported distribution or a failing package manager.
func failUnavailable(cause error, message string) *apperr.Error {
	return fail(cause, apperr.CodeUnavailable, http.StatusServiceUnavailable, message)
}

// notOwned builds the typed refusal for removing a package cashp did not
// install; the operator's own packages are never touched.
func notOwned(name string) error {
	return fail(ErrNotOwned, apperr.CodeForbidden, http.StatusForbidden,
		"this package was not installed by cashp and will not be removed").
		WithDetails(map[string]any{"package": name})
}

// distroUpgradeRefused builds the typed refusal for a whole-system upgrade.
func distroUpgradeRefused() error {
	return fail(ErrDistroUpgradeRefused, apperr.CodeForbidden, http.StatusForbidden,
		"a whole-system distribution upgrade is never performed")
}
