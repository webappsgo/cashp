package hostpkg

import (
	"net/http"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Multi-version PHP-FPM follows IDEA.md's mapping row plus its "Known
// platform gaps" section: no distribution ships more than one PHP version,
// so apt and dnf hosts need a pinned third-party repository, Alpine never
// packaged PHP 5.6/7.x at all, and Arch has no supported multi-version path.

// PHPNative is the version sentinel for a distribution that ships exactly one
// native PHP version, which is the only supported PHP on Arch.
const PHPNative = "native"

// alpinePHPFloor lists the Alpine release each PHP version first became
// available in. A version absent from the map was never packaged by Alpine.
var alpinePHPFloor = map[string]string{
	"8.0": "3.20",
	"8.1": "3.20",
	"8.2": "3.20",
	"8.3": "3.20",
	"8.4": "3.21",
	"8.5": "3.24",
}

// fedoraRemiFloor is the oldest Fedora release with an actively maintained
// Remi build; older releases are a degraded-support path, not a failure.
const fedoraRemiFloor = "43"

// PHPPlan is the resolved package set and repository requirement for one PHP
// version on one distribution.
type PHPPlan struct {
	// Version is the requested PHP version, or PHPNative on Arch.
	Version string
	// Packages is the PHP-FPM package set to install.
	Packages []string
	// Repo is the third-party repository that must be present first; it is
	// empty when the distribution needs none.
	Repo RepoID
	// Degraded marks a supported-but-unreliable path the caller should warn
	// about, such as Remi on an older Fedora.
	Degraded bool
	// DegradedReason explains the degradation in operator-facing terms.
	DegradedReason string
}

// PHPFPMPlan resolves the PHP-FPM packages for a version on a distribution.
func PHPFPMPlan(version string, d *Distro) (PHPPlan, error) {
	if d == nil {
		return PHPPlan{}, failUnavailable(ErrUnsupportedDistro, "host operating system is not supported")
	}

	if d.Family == FamilyArch {
		return archPHPPlan(version)
	}

	if err := ValidatePHPVersion(version); err != nil {
		return PHPPlan{}, err
	}
	compact := strings.ReplaceAll(version, ".", "")

	switch d.Family {
	case FamilyDebian:
		return PHPPlan{Version: version, Packages: []string{"php" + version + "-fpm"}, Repo: RepoSury}, nil
	case FamilyUbuntu:
		return PHPPlan{Version: version, Packages: []string{"php" + version + "-fpm"}, Repo: RepoOndrejPHP}, nil
	case FamilyAlpine:
		return alpinePHPPlan(version, compact, d)
	case FamilyRHEL:
		return PHPPlan{Version: version, Packages: []string{"php" + compact + "-php-fpm"}, Repo: RepoRemi}, nil
	case FamilyFedora:
		return fedoraPHPPlan(version, compact, d)
	default:
		return PHPPlan{}, phpNotAvailable(version, d, "this operating system has no supported PHP-FPM path")
	}
}

// archPHPPlan implements the Arch row: exactly one native PHP package, with
// every legacy version treated as unsupported rather than degraded, because
// the AUR builds IDEA.md describes are abandoned.
func archPHPPlan(version string) (PHPPlan, error) {
	if version != PHPNative && version != "" {
		return PHPPlan{}, fail(ErrServiceNotAvailable, apperr.CodeUnavailable, http.StatusServiceUnavailable,
			"only the current PHP version is supported on this operating system").
			WithDetails(map[string]any{"service": string(ServicePHPFPM), "php_version": version})
	}

	return PHPPlan{Version: PHPNative, Packages: []string{"php"}}, nil
}

// alpinePHPPlan implements the Alpine row and its documented gaps: PHP 5.6
// and 7.x were never packaged, and the newer versions each need a minimum
// Alpine release.
func alpinePHPPlan(version, compact string, d *Distro) (PHPPlan, error) {
	floor, ok := alpinePHPFloor[version]
	if !ok {
		return PHPPlan{}, phpNotAvailable(version, d, "this PHP version was never packaged for the host operating system")
	}
	if !d.AtLeast(floor) {
		return PHPPlan{}, phpNotAvailable(version, d, "this PHP version needs a newer host operating system release")
	}

	return PHPPlan{Version: version, Packages: []string{"php" + compact + "-fpm"}}, nil
}

// fedoraPHPPlan implements the Fedora row, marking releases whose Remi builds
// are archived as a degraded-support path.
func fedoraPHPPlan(version, compact string, d *Distro) (PHPPlan, error) {
	plan := PHPPlan{Version: version, Packages: []string{"php" + compact + "-php-fpm"}, Repo: RepoRemi}
	if !d.AtLeast(fedoraRemiFloor) {
		plan.Degraded = true
		plan.DegradedReason = "the third-party PHP repository no longer publishes builds for this operating system release"
	}

	return plan, nil
}

// phpNotAvailable builds the typed PHP availability failure.
func phpNotAvailable(version string, d *Distro, message string) error {
	return fail(ErrServiceNotAvailable, apperr.CodeUnavailable, http.StatusServiceUnavailable, message).
		WithDetails(map[string]any{
			"service":      string(ServicePHPFPM),
			"php_version":  version,
			"distribution": d.ID,
		})
}
