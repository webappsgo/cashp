// Package geoip implements cashp's built-in GeoIP support per AI.md PART
// 20. Databases come from sapics/ip-location-db via the jsDelivr CDN and
// are downloaded at runtime into {data_dir}/security/geoip — they are
// never embedded in the binary and MaxMind GeoLite2 is never used. Every
// lookup fails open: country is a risk signal that feeds the rest of the
// security pipeline, never the sole access gate.
package geoip

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// Attribution is the verbatim CC BY 4.0 notice required by AI.md PART 20
// on every page that displays GeoIP-derived data and in LICENSE.md's
// third-party section. Both notices are required together — the DB-IP
// notice alone does not cover the NRO-sourced country data, and vice
// versa. It must be rendered visibly, never behind a click-through,
// tooltip, or collapsed section.
const Attribution = `<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>

Country and ASN data licensed CC BY 4.0 by the Number Resource Organization (NRO).`

// AttributionHTML is the DB-IP half of Attribution, for templates that
// need the linked notice on its own line.
const AttributionHTML = `<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>`

// AttributionText is the NRO half of Attribution, for plain-text
// surfaces such as CLI output and log banners.
const AttributionText = `Country and ASN data licensed CC BY 4.0 by the Number Resource Organization (NRO).`

// Base URL of the jsDelivr CDN distribution of the @ip-location-db npm
// scope. No API key, account, or license agreement is required.
const cdnBase = "https://cdn.jsdelivr.net/npm"

// Local file names of the four downloaded databases, matching the file
// names published inside their npm packages.
const (
	fileASN     = "asn.mmdb"
	fileCity4   = "dbip-city-ipv4.mmdb"
	fileCity6   = "dbip-city-ipv6.mmdb"
	fileCountry = "geo-whois-asn-country.mmdb"
)

// source describes one downloadable database: the local file it is saved
// as and the jsDelivr URL it is fetched from.
type source struct {
	file string
	url  string
}

// sources lists every database AI.md PART 20 names as a canonical, single
// source per category. The country database exposes only country_code
// despite its package name; organization names come from the ASN
// database's autonomous_system_organization field.
var sources = []source{
	{file: fileASN, url: cdnBase + "/@ip-location-db/asn-mmdb/asn.mmdb"},
	{file: fileCountry, url: cdnBase + "/@ip-location-db/geo-whois-asn-country-mmdb/geo-whois-asn-country.mmdb"},
	{file: fileCity4, url: cdnBase + "/@ip-location-db/dbip-city-mmdb/dbip-city-ipv4.mmdb"},
	{file: fileCity6, url: cdnBase + "/@ip-location-db/dbip-city-mmdb/dbip-city-ipv6.mmdb"},
}

// Options configures a DB. A zero DataDir means no database directory is
// available, in which case every lookup misses and every request is
// allowed — the fail-open behaviour required by PART 20.
type Options struct {
	// DataDir is the server data directory; databases live in its
	// security/geoip subdirectory.
	DataDir string

	// Enabled turns GeoIP off entirely when false.
	Enabled bool

	// AllowCountries is the exclusive allowlist of ISO 3166-1 alpha-2
	// codes. When non-empty it wins over DenyCountries.
	AllowCountries []string

	// DenyCountries is the blocklist of ISO 3166-1 alpha-2 codes applied
	// only when AllowCountries is empty.
	DenyCountries []string
}

// DB holds the opened ip-location-db readers and the country policy. All
// methods are safe for concurrent use and safe to call on a nil receiver.
type DB struct {
	dir     string
	enabled bool

	allow map[string]struct{}
	deny  map[string]struct{}

	client *http.Client

	mu      sync.RWMutex
	readers map[string]*maxminddb.Reader
}

// New opens whichever databases are already present in
// {DataDir}/security/geoip. A missing database is not an error: GeoIP
// degrades to "no signal" until the scheduler's geoip_update task runs
// Update. An error is returned only when the database directory itself
// cannot be created.
func New(opts Options) (*DB, error) {
	d := &DB{
		enabled: opts.Enabled,
		allow:   normalizeCountries(opts.AllowCountries),
		deny:    normalizeCountries(opts.DenyCountries),
		client:  &http.Client{Timeout: 5 * time.Minute},
		readers: make(map[string]*maxminddb.Reader),
	}

	if opts.DataDir == "" {
		return d, nil
	}

	d.dir = filepath.Join(opts.DataDir, "security", "geoip")
	if err := os.MkdirAll(d.dir, 0o750); err != nil {
		return nil, err
	}

	for _, src := range sources {
		d.openReader(src.file)
	}

	return d, nil
}

// Dir reports the directory the .mmdb files are stored in, or an empty
// string when no data directory was configured.
func (d *DB) Dir() string {
	if d == nil {
		return ""
	}

	return d.dir
}

// Enabled reports whether GeoIP is turned on.
func (d *DB) Enabled() bool {
	return d != nil && d.enabled
}

// Close releases every open database reader. It is safe to call more than
// once and on a nil receiver.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var firstErr error

	for name, r := range d.readers {
		if err := r.Close(); err != nil && firstErr == nil {
			firstErr = err
		}

		delete(d.readers, name)
	}

	return firstErr
}

// openReader opens one database file and stores its reader. A missing or
// unreadable file is ignored so a partial download never breaks lookups.
func (d *DB) openReader(file string) {
	r, err := maxminddb.Open(filepath.Join(d.dir, file))
	if err != nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if old, ok := d.readers[file]; ok {
		_ = old.Close()
	}

	d.readers[file] = r
}

// reader returns the reader for a database file, or nil when that
// database has not been downloaded yet.
func (d *DB) reader(file string) *maxminddb.Reader {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.readers[file]
}

// normalizeCountries upper-cases and de-duplicates a country list,
// discarding anything that is not a two-letter ISO 3166-1 alpha-2 code.
func normalizeCountries(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))

	for _, c := range in {
		code := strings.ToUpper(strings.TrimSpace(c))
		if !validCountryCode(code) {
			continue
		}

		out[code] = struct{}{}
	}

	return out
}

// validCountryCode reports whether code is two ASCII letters, the only
// shape AI.md PART 20 accepts for country policy entries.
func validCountryCode(code string) bool {
	if len(code) != 2 {
		return false
	}

	for i := 0; i < len(code); i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}

	return true
}
