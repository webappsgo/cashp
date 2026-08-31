package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

// DefaultCSP is the baseline Content-Security-Policy. It allows no inline
// script of any kind, which is what makes the no-JavaScript-first rule
// enforceable rather than advisory.
const DefaultCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'self'; " +
	"form-action 'self'; base-uri 'self'; object-src 'none'"

// DefaultPermissionsPolicy blocks the sensor and tracking features cashp
// does not use, and scopes the features the spec itself needs to this
// origin.
const DefaultPermissionsPolicy = "accelerometer=(), ambient-light-sensor=(), attribution-reporting=(), " +
	"autoplay=(self), browsing-topics=(), camera=(), display-capture=(), encrypted-media=(self), " +
	"fullscreen=(self), geolocation=(), gyroscope=(), interest-cohort=(), magnetometer=(), " +
	"microphone=(), midi=(), payment=(self), picture-in-picture=(self), publickey-credentials-get=(self), " +
	"screen-wake-lock=(), serial=(), storage-access=(self), usb=(), web-share=(self)"

// DefaultHSTSMaxAge is two years, the value the HSTS preload list requires.
const DefaultHSTSMaxAge = 63072000

// HeaderOptions configures the security headers applied to every response.
type HeaderOptions struct {
	// CSP overrides DefaultCSP when set.
	CSP string
	// PermissionsPolicy overrides DefaultPermissionsPolicy when set. An
	// explicit "-" disables the header.
	PermissionsPolicy string
	// COOP, COEP, and CORP carry the cross-origin isolation policies.
	COOP string
	COEP string
	CORP string
	// HSTS enables Strict-Transport-Security. It is honoured only when TLS
	// is enabled and never on an overlay request.
	HSTS bool
	// HSTSMaxAge is the max-age in seconds; zero uses DefaultHSTSMaxAge.
	HSTSMaxAge int
	// HSTSIncludeSubdomains and HSTSPreload set the matching directives.
	HSTSIncludeSubdomains bool
	HSTSPreload           bool
	// TLS reports whether the deployment terminates TLS.
	TLS bool
	// ReportingEndpoint is the absolute URL that receives CSP, NEL, and
	// deprecation reports. Empty disables the reporting headers.
	ReportingEndpoint string
	// OnionAddress is the .onion hostname advertised to clearnet visitors.
	// Empty disables the advertisement.
	OnionAddress string
}

// SecurityHeaders applies the PART 11 header set to every response.
//
// Overlay requests are deliberately different: a Tor or I2P visitor never
// receives Strict-Transport-Security, never receives Onion-Location, and is
// never redirected to HTTPS, because each of those would either take the
// hidden service offline or leak the visitor back onto the clearnet.
func SecurityHeaders(opts HeaderOptions) func(http.Handler) http.Handler {
	base := staticHeaders(opts)
	hsts := hstsValue(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for k, v := range base {
				h.Set(k, v)
			}
			overlay := IsOverlayRequest(r.Context()) || IsOverlayHost(r.Host)
			if !overlay {
				if hsts != "" {
					h.Set("Strict-Transport-Security", hsts)
				}
				if opts.OnionAddress != "" && wantsHTML(r) {
					h.Set("Onion-Location", "http://"+opts.OnionAddress+r.URL.RequestURI())
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// staticHeaders builds the headers that are identical on every response.
func staticHeaders(opts HeaderOptions) map[string]string {
	h := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "SAMEORIGIN",
		"X-XSS-Protection":                  "1; mode=block",
		"Referrer-Policy":                   "strict-origin-when-cross-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Origin-Agent-Cluster":              "?1",
		"Cross-Origin-Opener-Policy":        valueOr(opts.COOP, "unsafe-none"),
		"Cross-Origin-Embedder-Policy":      valueOr(opts.COEP, "unsafe-none"),
		"Cross-Origin-Resource-Policy":      valueOr(opts.CORP, "cross-origin"),
		"Content-Security-Policy":           valueOr(opts.CSP, DefaultCSP),
	}
	if pp := valueOr(opts.PermissionsPolicy, DefaultPermissionsPolicy); pp != "-" {
		h["Permissions-Policy"] = pp
	}
	if endpoint := strings.TrimSpace(opts.ReportingEndpoint); endpoint != "" {
		h["Reporting-Endpoints"] = fmt.Sprintf("default=%q", endpoint)
		h["Report-To"] = fmt.Sprintf(`{"group":"default","max_age":10886400,"endpoints":[{"url":%q}]}`, endpoint)
		h["NEL"] = `{"report_to":"default","max_age":2592000,"include_subdomains":true}`
	}
	return h
}

// hstsValue builds the Strict-Transport-Security value, or an empty string
// when HSTS must not be sent at all.
func hstsValue(opts HeaderOptions) string {
	if !opts.HSTS || !opts.TLS {
		return ""
	}
	maxAge := opts.HSTSMaxAge
	if maxAge == 0 {
		maxAge = DefaultHSTSMaxAge
	}
	if maxAge < 0 {
		return ""
	}
	value := fmt.Sprintf("max-age=%d", maxAge)
	if opts.HSTSIncludeSubdomains {
		value += "; includeSubDomains"
	}
	if opts.HSTSPreload {
		value += "; preload"
	}
	return value
}

// wantsHTML reports whether the request is a top-level document navigation,
// which is the only response class that may advertise Onion-Location.
func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// valueOr returns the override when set, otherwise the default.
func valueOr(override, fallback string) string {
	if strings.TrimSpace(override) == "" {
		return fallback
	}
	return override
}
