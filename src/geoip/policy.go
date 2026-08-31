package geoip

import (
	"net"
	"sort"
)

// Allowed applies the configured country policy to ip. It is advisory
// only: country data is trivially bypassed with a VPN, proxy, or Tor, so
// a true result never means a request may skip rate limiting,
// authentication, authorization, input validation, or audit logging.
//
// The policy fails open. Allowed returns true whenever GeoIP is
// disabled, the address is private or internal, the country database is
// missing, or the lookup misses — a GeoIP failure must never be the
// reason a request is rejected.
//
// When both lists are configured AllowCountries wins: the deny list is
// ignored and only listed countries are allowed.
func (d *DB) Allowed(ip net.IP) bool {
	if !d.Enabled() {
		return true
	}

	if len(d.allow) == 0 && len(d.deny) == 0 {
		return true
	}

	if len(ip) == 0 || IsInternal(ip) {
		return true
	}

	code, ok := d.Country(ip)
	if !ok || code == "" {
		return true
	}

	return d.CountryAllowed(code)
}

// CountryAllowed applies the country policy to an already-resolved ISO
// 3166-1 alpha-2 code. An unrecognized or empty code is allowed, keeping
// the fail-open contract for callers that resolved the country
// themselves.
func (d *DB) CountryAllowed(code string) bool {
	if d == nil {
		return true
	}

	normalized := normalizeCode(code)
	if normalized == "" {
		return true
	}

	if len(d.allow) > 0 {
		_, ok := d.allow[normalized]

		return ok
	}

	if len(d.deny) > 0 {
		_, ok := d.deny[normalized]

		return !ok
	}

	return true
}

// PolicyMode reports which country policy is in effect: "allowlist",
// "denylist", or "open" when neither list is configured.
func (d *DB) PolicyMode() string {
	if d == nil {
		return "open"
	}

	switch {
	case len(d.allow) > 0:
		return "allowlist"
	case len(d.deny) > 0:
		return "denylist"
	}

	return "open"
}

// AllowCountries returns the normalized, sorted exclusive allowlist.
func (d *DB) AllowCountries() []string {
	if d == nil {
		return nil
	}

	return sortedCodes(d.allow)
}

// DenyCountries returns the normalized, sorted blocklist. It is inert
// while an allowlist is configured.
func (d *DB) DenyCountries() []string {
	if d == nil {
		return nil
	}

	return sortedCodes(d.deny)
}

// sortedCodes flattens a country set into a stable, sorted slice.
func sortedCodes(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}

	out := make([]string, 0, len(set))
	for code := range set {
		out = append(out, code)
	}

	sort.Strings(out)

	return out
}
