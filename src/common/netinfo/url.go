package netinfo

import (
	"fmt"
	"net/http"
	"strings"
)

// GetURLVars resolves {proto}, {fqdn}, and {port} for a request. Reverse
// proxy headers are preferred, a Tor request short-circuits the whole
// chain, and the port is an empty string whenever it is the default for the
// protocol, so :80 and :443 never appear in a URL.
func GetURLVars(r *http.Request) (proto, fqdn, port string) {
	opts := Settings()

	requestHost, requestPort := splitHostPort(hostOf(r))

	// Priority 0: a Tor request is answered entirely from tor config, with
	// no proxy header inspection and no trusted peer check.
	if opts.OnionAddress != "" && strings.EqualFold(requestHost, opts.OnionAddress) {
		return "http", strings.ToLower(opts.OnionAddress), ""
	}

	proto = ProxyProto(r)
	if proto == "" {
		if r != nil && r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	fqdn = ProxyFQDN(r)
	if fqdn != "" {
		Observe(fqdn, proto)
	} else if requestHost != "" && IsValidHost(requestHost, opts.DevMode, opts.ProjectName) {
		fqdn = strings.ToLower(requestHost)
	} else {
		fqdn = DetectFQDN()
	}

	port = ProxyPort(r)
	if port == "" {
		port = requestPort
	}
	if port == "" {
		port = opts.ListenPort
	}
	if port == "" {
		port = defaultPort(proto)
	}

	return proto, fqdn, StripDefaultPort(proto, port)
}

// StripDefaultPort returns an empty string when the port is the default for
// the protocol, and the port unchanged otherwise.
func StripDefaultPort(proto, port string) string {
	port = strings.TrimSpace(port)
	if port == "" || port == defaultPort(proto) {
		return ""
	}
	return port
}

// BasePath returns the URL prefix the app is mounted under, with no
// trailing slash. It is an empty string when the app is served at the root.
func BasePath(r *http.Request) string {
	return ProxyBasePath(r)
}

// BuildURL builds an absolute URL for an app-absolute path, prefixing the
// detected base path and never including :80 or :443. The path must not
// already contain the proxy prefix.
func BuildURL(r *http.Request, path string) string {
	proto, fqdn, port := GetURLVars(r)

	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = BasePath(r) + path

	if port == "" {
		return fmt.Sprintf("%s://%s%s", proto, fqdn, path)
	}
	return fmt.Sprintf("%s://%s:%s%s", proto, fqdn, port, path)
}

// Origin returns the scheme and authority of the request with no path,
// suitable for CORS allow-lists.
func Origin(r *http.Request) string {
	return BuildURL(r, "")
}

// hostOf returns the Host of a request, tolerating a nil request.
func hostOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

// defaultPort returns the well-known port for a protocol.
func defaultPort(proto string) string {
	if proto == "https" {
		return "443"
	}
	return "80"
}
