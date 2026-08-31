package web

import (
	"net/http"
	"strings"
)

// ClientType is the representation a client is best served with.
type ClientType string

// Supported client representations.
const (
	// ClientHTML is a graphical browser: full HTML, CSS and enhancements.
	ClientHTML ClientType = "html"
	// ClientText is a terminal browser or an unidentified client: plain text.
	ClientText ClientType = "text"
	// ClientJSON is an API consumer or one of our own CLI binaries.
	ClientJSON ClientType = "json"
)

// browserAgents identify graphical browsers.
var browserAgents = []string{
	"Mozilla/", "Chrome/", "Safari/", "Edge/", "Firefox/", "Opera/", "MSIE ", "Trident/",
}

// cliAgents identify generic non-interactive HTTP clients (curl/wget/httpie
// and friends). Per AI.md PART 16 "Smart Content Detection" and PART 14's
// isNonInteractiveClient, these get plain text, not JSON — JSON on frontend
// routes is reserved for an explicit Accept: application/json or one of our
// own binaries (ourCLIAgents below), which know how to parse it.
var cliAgents = []string{
	"curl/", "Wget/", "HTTPie/", "python-requests/", "Go-http-client/", "node-fetch/",
	"okhttp/", "PostmanRuntime/", "insomnia/", "libwww-perl/",
}

// textBrowserAgents identify non-graphical browsers, which get HTML without any
// JavaScript dependency but never the plain-text API representation.
var textBrowserAgents = []string{
	"Lynx/", "w3m/", "ELinks/", "Links (", "Links/", "NetSurf/", "eww/", "Browsh/",
}

// ourCLIAgents identify the binaries shipped with this project.
var ourCLIAgents = []string{"cashp/", "cashp-cli/", "cashp-agent/"}

// DetectClientType picks the representation for a request from the Accept
// header first and the User-Agent second, following AI.md PART 16 Smart
// Content Detection. HTML is the default so an unknown browser is never handed
// a raw JSON body.
func DetectClientType(req *http.Request) ClientType {
	if req == nil {
		return ClientHTML
	}

	accept := req.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "application/json"):
		return ClientJSON
	case strings.Contains(accept, "text/html"):
		return ClientHTML
	case accept == "text/plain":
		return ClientText
	}

	agent := req.Header.Get("User-Agent")
	switch {
	case agent == "":
		return ClientText
	case matchesAgent(agent, ourCLIAgents):
		return ClientJSON
	case matchesAgent(agent, textBrowserAgents):
		return ClientHTML
	case matchesAgent(agent, browserAgents):
		return ClientHTML
	case matchesAgent(agent, cliAgents):
		return ClientText
	default:
		return ClientHTML
	}
}

// IsTextBrowser reports whether the request comes from a non-graphical browser
// such as lynx or w3m, which must never be sent JavaScript-dependent markup.
func IsTextBrowser(req *http.Request) bool {
	if req == nil {
		return false
	}
	return matchesAgent(req.Header.Get("User-Agent"), textBrowserAgents)
}

// matchesAgent reports whether the user agent contains any of the markers.
func matchesAgent(agent string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(agent, marker) {
			return true
		}
	}
	return false
}
