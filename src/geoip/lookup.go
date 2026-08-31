package geoip

import (
	"net"
	"strconv"
	"strings"
)

// Result is everything cashp exposes about an IP address. Every field is
// best-effort: a missing database or a database miss leaves the
// corresponding fields at their zero value rather than producing an
// error.
type Result struct {
	// CountryCode is the ISO 3166-1 alpha-2 code from the country
	// database, falling back to the city database's country_code.
	CountryCode string

	// CountryName is the English ISO 3166-1 name for CountryCode.
	CountryName string

	// City is the city name from the DB-IP city database.
	City string

	// ASN is the autonomous_system_number from the ASN database.
	ASN uint

	// ASOrg is the autonomous_system_organization from the ASN database —
	// a BGP/RIR-derived AS holder name, not a registrant record.
	ASOrg string

	// Latitude and Longitude come from the DB-IP city database.
	Latitude  float64
	Longitude float64
}

// Lookup resolves ip against every available database. ok is false when
// GeoIP is disabled, the address is private, internal, or loopback, no
// database is present, or every database missed. Lookup never returns an
// error and never signals that a request should be blocked.
func (d *DB) Lookup(ip net.IP) (Result, bool) {
	var res Result

	if !d.Enabled() || len(ip) == 0 {
		return res, false
	}

	if IsInternal(ip) {
		return res, false
	}

	found := false

	if rec, ok := d.record(fileCountry, ip); ok {
		if code := normalizeCode(asString(rec["country_code"])); code != "" {
			res.CountryCode = code
			found = true
		}
	}

	if rec, ok := d.record(cityFile(ip), ip); ok {
		if res.CountryCode == "" {
			if code := normalizeCode(asString(rec["country_code"])); code != "" {
				res.CountryCode = code
				found = true
			}
		}

		if city := strings.TrimSpace(asString(rec["city"])); city != "" {
			res.City = city
			found = true
		}

		res.Latitude = asFloat(rec["latitude"])
		res.Longitude = asFloat(rec["longitude"])

		if res.Latitude != 0 || res.Longitude != 0 {
			found = true
		}
	}

	if rec, ok := d.record(fileASN, ip); ok {
		if n := asUint(rec["autonomous_system_number"]); n != 0 {
			res.ASN = n
			found = true
		}

		if org := strings.TrimSpace(asString(rec["autonomous_system_organization"])); org != "" {
			res.ASOrg = org
			found = true
		}
	}

	res.CountryName = CountryName(res.CountryCode)

	return res, found
}

// Country resolves only the ISO 3166-1 alpha-2 code for ip, skipping the
// city and ASN databases. ok is false for internal addresses and misses.
func (d *DB) Country(ip net.IP) (string, bool) {
	if !d.Enabled() || len(ip) == 0 || IsInternal(ip) {
		return "", false
	}

	if rec, ok := d.record(fileCountry, ip); ok {
		if code := normalizeCode(asString(rec["country_code"])); code != "" {
			return code, true
		}
	}

	if rec, ok := d.record(cityFile(ip), ip); ok {
		if code := normalizeCode(asString(rec["country_code"])); code != "" {
			return code, true
		}
	}

	return "", false
}

// record decodes one database entry into a generic map. Decoding into a
// map rather than a fixed struct keeps a single unexpected field type
// from failing the whole lookup.
func (d *DB) record(file string, ip net.IP) (map[string]any, bool) {
	r := d.reader(file)
	if r == nil {
		return nil, false
	}

	rec := make(map[string]any)
	if err := r.Lookup(ip, &rec); err != nil || len(rec) == 0 {
		return nil, false
	}

	return rec, true
}

// cityFile picks the city database matching the address family, since
// ip-location-db ships DB-IP city data as separate IPv4 and IPv6 files.
func cityFile(ip net.IP) string {
	if ip.To4() != nil {
		return fileCity4
	}

	return fileCity6
}

// carrierGradeNAT is RFC 6598 shared address space, which is internal
// infrastructure and never has meaningful country data.
var carrierGradeNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// thisNetwork is RFC 1122 "this network", which is never routable.
var thisNetwork = &net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(8, 32)}

// IsInternal reports whether ip is private, loopback, link-local,
// multicast, or otherwise non-routable. AI.md PART 20 forbids looking up
// or country-blocking these addresses.
func IsInternal(ip net.IP) bool {
	if len(ip) == 0 {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}

	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}

	if ip.IsMulticast() {
		return true
	}

	if v4 := ip.To4(); v4 != nil {
		return carrierGradeNAT.Contains(v4) || thisNetwork.Contains(v4)
	}

	return false
}

// normalizeCode upper-cases a database country code and rejects anything
// that is not a two-letter ISO 3166-1 alpha-2 value.
func normalizeCode(v string) string {
	code := strings.ToUpper(strings.TrimSpace(v))
	if !validCountryCode(code) {
		return ""
	}

	return code
}

// asString coerces a decoded database value to a string.
func asString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}

	return s
}

// asFloat coerces a decoded database value to a float64, accepting the
// numeric and string encodings ip-location-db files use.
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}

		return f
	}

	return 0
}

// asUint coerces a decoded database value to a uint, accepting the
// numeric and string encodings ip-location-db files use.
func asUint(v any) uint {
	switch n := v.(type) {
	case uint:
		return n
	case uint16:
		return uint(n)
	case uint32:
		return uint(n)
	case uint64:
		return uint(n)
	case int:
		if n < 0 {
			return 0
		}

		return uint(n)
	case int32:
		if n < 0 {
			return 0
		}

		return uint(n)
	case int64:
		if n < 0 {
			return 0
		}

		return uint(n)
	case float64:
		if n < 0 {
			return 0
		}

		return uint(n)
	case string:
		u, err := strconv.ParseUint(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0
		}

		return uint(u)
	}

	return 0
}
