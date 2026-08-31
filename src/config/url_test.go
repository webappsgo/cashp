package config

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", "/"},
		{"/", "/"},
		{"panel", "/panel/"},
		{"/panel", "/panel/"},
		{"/panel/", "/panel/"},
		{"//panel//admin//", "/panel/admin/"},
		{"  /panel  ", "/panel/"},
	} {
		if got := NormalizeBaseURL(tc.in); got != tc.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveFQDNPrefersTrustedForwardedHost(t *testing.T) {
	cfg := Defaults()
	cfg.Server.FQDN = "configured.test"

	headers := ProxyHeaders{Host: "internal.test:8080", ForwardedHost: "public.test"}

	if got := cfg.ResolveFQDN(headers, true); got != "public.test" {
		t.Errorf("trusted ResolveFQDN() = %q, want public.test", got)
	}
	if got := cfg.ResolveFQDN(headers, false); got != "internal.test" {
		t.Errorf("untrusted ResolveFQDN() = %q, want internal.test", got)
	}
}

func TestResolveFQDNFallsBackToConfig(t *testing.T) {
	cfg := Defaults()
	cfg.Server.FQDN = "configured.test"

	if got := cfg.ResolveFQDN(ProxyHeaders{}, true); got != "configured.test" {
		t.Errorf("ResolveFQDN() = %q, want configured.test", got)
	}
}

func TestResolveFQDNRejectsInjectedHost(t *testing.T) {
	cfg := Defaults()
	cfg.Server.FQDN = "configured.test"

	headers := ProxyHeaders{ForwardedHost: "evil.test/\r\nX-Injected: 1"}
	if got := cfg.ResolveFQDN(headers, true); got != "configured.test" {
		t.Errorf("ResolveFQDN() = %q, want the injected host to be ignored", got)
	}
}

func TestDetectFQDNUsesDomainEnv(t *testing.T) {
	t.Setenv("DOMAIN", "env.test")

	if got := DetectFQDN(); got != "env.test" {
		t.Errorf("DetectFQDN() = %q, want env.test", got)
	}
}

func TestDetectFQDNNeverEmpty(t *testing.T) {
	t.Setenv("DOMAIN", "")
	t.Setenv("HOSTNAME", "")

	if got := DetectFQDN(); got == "" {
		t.Error("DetectFQDN() must always return a usable name")
	}
}

func TestResolveBaseURLPrecedence(t *testing.T) {
	cfg := Defaults()
	cfg.Server.BaseURL = "/configured/"

	headers := ProxyHeaders{ForwardedPrefix: "/prefix", ForwardedPath: "/path", ScriptName: "/script"}
	if got := cfg.ResolveBaseURL(headers, true); got != "/prefix/" {
		t.Errorf("ResolveBaseURL() = %q, want /prefix/", got)
	}

	headers.ForwardedPrefix = ""
	if got := cfg.ResolveBaseURL(headers, true); got != "/path/" {
		t.Errorf("ResolveBaseURL() = %q, want /path/", got)
	}

	headers.ForwardedPath = ""
	if got := cfg.ResolveBaseURL(headers, true); got != "/script/" {
		t.Errorf("ResolveBaseURL() = %q, want /script/", got)
	}

	headers.ScriptName = ""
	if got := cfg.ResolveBaseURL(headers, true); got != "/configured/" {
		t.Errorf("ResolveBaseURL() = %q, want /configured/", got)
	}
}

func TestResolveBaseURLIgnoresUntrustedHeaders(t *testing.T) {
	cfg := Defaults()

	headers := ProxyHeaders{ForwardedPrefix: "/attacker"}
	if got := cfg.ResolveBaseURL(headers, false); got != "/" {
		t.Errorf("ResolveBaseURL() = %q, want / for an untrusted peer", got)
	}
}

func TestResolveScheme(t *testing.T) {
	cfg := Defaults()

	if got := cfg.ResolveScheme(ProxyHeaders{ForwardedProto: "https"}, true); got != "https" {
		t.Errorf("ResolveScheme() = %q, want https", got)
	}
	if got := cfg.ResolveScheme(ProxyHeaders{ForwardedProto: "https"}, false); got != "http" {
		t.Errorf("ResolveScheme() = %q, want http for an untrusted peer", got)
	}
	if got := cfg.ResolveScheme(ProxyHeaders{TLS: true}, false); got != "https" {
		t.Errorf("ResolveScheme() = %q, want https for a local TLS connection", got)
	}

	cfg.Server.SSL.Enabled = NewBool(true)
	if got := cfg.ResolveScheme(ProxyHeaders{}, false); got != "https" {
		t.Errorf("ResolveScheme() = %q, want https when SSL is enabled", got)
	}
}

func TestResolveAppURLAndAdminURL(t *testing.T) {
	cfg := Defaults()
	cfg.Server.FQDN = "panel.test"

	headers := ProxyHeaders{Host: "panel.test"}
	if got := cfg.ResolveAppURL(headers, false); got != "http://panel.test" {
		t.Errorf("ResolveAppURL() = %q, want http://panel.test", got)
	}
	if got := cfg.AdminURL(headers, false); got != "http://panel.test/administration" {
		t.Errorf("AdminURL() = %q, want http://panel.test/administration", got)
	}

	cfg.Server.BaseURL = "/panel/"
	if got := cfg.ResolveAppURL(headers, false); got != "http://panel.test/panel" {
		t.Errorf("ResolveAppURL() = %q, want http://panel.test/panel", got)
	}
}

func TestAPIPrefix(t *testing.T) {
	cfg := Defaults()

	if got := cfg.APIPrefix(); got != "/api/v1" {
		t.Errorf("APIPrefix() = %q, want /api/v1", got)
	}

	cfg.Server.BaseURL = "/panel/"
	cfg.Server.APIVersion = "v2"
	if got := cfg.APIPrefix(); got != "/panel/api/v2" {
		t.Errorf("APIPrefix() = %q, want /panel/api/v2", got)
	}
}
