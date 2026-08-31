package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectClientTypeFromAccept(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		want   ClientType
	}{
		{"json api", "application/json", ClientJSON},
		{"json with quality", "application/json;q=0.9, */*;q=0.1", ClientJSON},
		{"browser", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", ClientHTML},
		{"plain text", "text/plain", ClientText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
			req.Header.Set("Accept", tc.accept)
			if got := DetectClientType(req); got != tc.want {
				t.Errorf("DetectClientType = %q, want %q", got, tc.want)
			}
		})
	}
}

// With no usable Accept header the User-Agent decides.
func TestDetectClientTypeFromUserAgent(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		want  ClientType
	}{
		{"firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0", ClientHTML},
		{"chrome", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36", ClientHTML},
		{"lynx", "Lynx/2.9.0dev.12 libwww-FM/2.14", ClientHTML},
		{"w3m", "w3m/0.5.3+git20230121", ClientHTML},
		{"curl", "curl/8.8.0", ClientText},
		{"wget", "Wget/1.24.5", ClientText},
		{"our cli", "cashp-cli/1.0.0", ClientJSON},
		{"our agent", "cashp-agent/1.0.0", ClientJSON},
		{"empty", "", ClientText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
			req.Header.Del("Accept")
			if tc.agent != "" {
				req.Header.Set("User-Agent", tc.agent)
			} else {
				req.Header.Del("User-Agent")
			}
			if got := DetectClientType(req); got != tc.want {
				t.Errorf("DetectClientType = %q, want %q", got, tc.want)
			}
		})
	}
}

// A terminal browser sends Accept: text/html, so it must still be served HTML
// rather than the plain-text representation.
func TestDetectClientTypeTextBrowserGetsHTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	req.Header.Set("Accept", "text/html, text/*;q=0.5")
	req.Header.Set("User-Agent", "Lynx/2.9.0dev.12 libwww-FM/2.14")

	if got := DetectClientType(req); got != ClientHTML {
		t.Errorf("DetectClientType = %q, want %q", got, ClientHTML)
	}
	if !IsTextBrowser(req) {
		t.Error("IsTextBrowser = false for lynx")
	}
}

func TestDetectClientTypeUnknownDefaultsToHTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	req.Header.Del("Accept")
	req.Header.Set("User-Agent", "SomeUnknownClient/3.1")

	if got := DetectClientType(req); got != ClientHTML {
		t.Errorf("DetectClientType = %q, want %q", got, ClientHTML)
	}
}

func TestDetectClientTypeNilRequest(t *testing.T) {
	if got := DetectClientType(nil); got != ClientHTML {
		t.Errorf("DetectClientType(nil) = %q, want %q", got, ClientHTML)
	}
	if IsTextBrowser(nil) {
		t.Error("IsTextBrowser(nil) = true")
	}
}

func TestIsTextBrowserRejectsGraphicalBrowsers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/128.0")
	if IsTextBrowser(req) {
		t.Error("IsTextBrowser = true for firefox")
	}
}
