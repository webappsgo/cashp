package config

import (
	"net"
	"os"
	"strings"
)

// ProxyHeaders carries the request headers that participate in URL
// resolution. The HTTP layer fills this in; every resolver here is a pure
// function of it plus the config, so none of them import net/http.
type ProxyHeaders struct {
	// Host is the request's Host header.
	Host string
	// ForwardedHost is X-Forwarded-Host.
	ForwardedHost string
	// ForwardedProto is X-Forwarded-Proto.
	ForwardedProto string
	// ForwardedPrefix is X-Forwarded-Prefix.
	ForwardedPrefix string
	// ForwardedPath is X-Forwarded-Path.
	ForwardedPath string
	// ScriptName is X-Script-Name.
	ScriptName string
	// TLS reports whether the connection reached this process over TLS.
	TLS bool
}

// ResolveFQDN returns the hostname the request should be answered as.
// Reverse-proxy headers win, but only when the peer that sent them is a
// trusted proxy; otherwise a client could forge the site's identity. The
// remaining chain is the configured fqdn (which the DOMAIN environment
// variable populates), the system hostname, a global IP, then localhost.
func (c *Config) ResolveFQDN(h ProxyHeaders, trustedPeer bool) string {
	if trustedPeer {
		if host := hostOnly(firstValue(h.ForwardedHost)); host != "" {
			return host
		}
	}

	if host := hostOnly(h.Host); host != "" {
		return host
	}

	if c != nil && c.Server.FQDN != "" {
		return c.Server.FQDN
	}

	return DetectFQDN()
}

// DetectFQDN resolves the server's own hostname without any request
// context: DOMAIN, the system hostname, HOSTNAME, a globally routable
// interface address, then localhost.
func DetectFQDN() string {
	if domain := hostOnly(os.Getenv("DOMAIN")); domain != "" {
		return domain
	}

	if name, err := os.Hostname(); err == nil {
		if host := hostOnly(name); host != "" && host != "localhost" {
			return host
		}
	}

	if name := hostOnly(os.Getenv("HOSTNAME")); name != "" && name != "localhost" {
		return name
	}

	if ip := GlobalIP(); ip != "" {
		return ip
	}

	return "localhost"
}

// GlobalIP returns the first globally routable unicast address on any
// interface, preferring IPv4 because it is what operators paste into a
// browser. It returns an empty string when the host has none.
func GlobalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	var fallback string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || !ipNet.IP.IsGlobalUnicast() || ipNet.IP.IsPrivate() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
		if fallback == "" {
			fallback = ipNet.IP.String()
		}
	}

	return fallback
}

// ResolveBaseURL returns the path prefix the application is mounted under,
// always starting and ending with a slash. Proxy-supplied prefixes are
// honored only from a trusted peer; the order is X-Forwarded-Prefix,
// X-Forwarded-Path, X-Script-Name, the configured baseurl, then "/".
func (c *Config) ResolveBaseURL(h ProxyHeaders, trustedPeer bool) string {
	if trustedPeer {
		for _, candidate := range []string{h.ForwardedPrefix, h.ForwardedPath, h.ScriptName} {
			if prefix := NormalizeBaseURL(candidate); prefix != "/" {
				return prefix
			}
		}
	}

	if c != nil {
		return NormalizeBaseURL(c.Server.BaseURL)
	}

	return "/"
}

// NormalizeBaseURL forces a path prefix into the canonical
// leading-and-trailing-slash form, collapsing repeated slashes and mapping
// an empty or root value to "/".
func NormalizeBaseURL(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "/"
	}

	var parts []string
	for _, segment := range strings.Split(prefix, "/") {
		if segment != "" && segment != "." {
			parts = append(parts, segment)
		}
	}

	if len(parts) == 0 {
		return "/"
	}

	return "/" + strings.Join(parts, "/") + "/"
}

// ResolveScheme returns the scheme the client used. X-Forwarded-Proto is
// consulted only for trusted peers; otherwise the local connection decides.
func (c *Config) ResolveScheme(h ProxyHeaders, trustedPeer bool) string {
	if trustedPeer {
		switch strings.ToLower(firstValue(h.ForwardedProto)) {
		case "https":
			return "https"
		case "http":
			return "http"
		}
	}

	if h.TLS {
		return "https"
	}

	if c != nil && c.Server.SSL.Enabled.Value {
		return "https"
	}

	return "http"
}

// ResolveAppURL builds the absolute base URL of this application for the
// given request: scheme, host, and mount prefix, without a trailing slash
// unless the application is mounted at the root.
func (c *Config) ResolveAppURL(h ProxyHeaders, trustedPeer bool) string {
	url := c.ResolveScheme(h, trustedPeer) + "://" + c.ResolveFQDN(h, trustedPeer)

	prefix := c.ResolveBaseURL(h, trustedPeer)
	if prefix == "/" {
		return url
	}

	return url + strings.TrimSuffix(prefix, "/")
}

// AdminURL returns the absolute URL of the admin panel for the given
// request, honoring the configured admin_path.
func (c *Config) AdminURL(h ProxyHeaders, trustedPeer bool) string {
	path := DefaultAdminPath
	if c != nil && c.Server.AdminPath != "" {
		path = c.Server.AdminPath
	}

	return c.ResolveAppURL(h, trustedPeer) + "/" + path
}

// APIPrefix returns the mount path of the versioned API, such as "/api/v1".
func (c *Config) APIPrefix() string {
	version := DefaultAPIVersion
	if c != nil && c.Server.APIVersion != "" {
		version = c.Server.APIVersion
	}

	prefix := "/"
	if c != nil {
		prefix = NormalizeBaseURL(c.Server.BaseURL)
	}

	return strings.TrimSuffix(prefix, "/") + "/api/" + version
}

// hostOnly strips any port and surrounding whitespace from a host value and
// rejects anything carrying header-injection characters.
func hostOnly(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n/\\") {
		return ""
	}

	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}

	return strings.Trim(strings.ToLower(value), "[]")
}

// firstValue returns the first entry of a comma-separated header value,
// which is the hop closest to the client.
func firstValue(value string) string {
	if idx := strings.IndexByte(value, ','); idx >= 0 {
		return value[:idx]
	}
	return value
}
