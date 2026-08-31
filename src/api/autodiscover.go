package api

import (
	"fmt"
	"net/http"
	"strings"
)

// AutodiscoverEndpoints lists the well-known entry points a CLI or agent
// needs in order to configure itself against this instance.
type AutodiscoverEndpoints struct {
	Health       string `json:"health"`
	Ready        string `json:"ready"`
	Version      string `json:"version"`
	OpenAPI      string `json:"openapi"`
	GraphQL      string `json:"graphql"`
	Autodiscover string `json:"autodiscover"`
}

// AutodiscoverAuth describes how a client authenticates.
type AutodiscoverAuth struct {
	Methods     []string `json:"methods"`
	Header      string   `json:"header"`
	Scheme      string   `json:"scheme"`
	TokenPrefix string   `json:"token_prefix"`
}

// AutodiscoverPagination describes the collection conventions.
type AutodiscoverPagination struct {
	DefaultLimit int      `json:"default_limit"`
	MaxLimit     int      `json:"max_limit"`
	Params       []string `json:"params"`
}

// AutodiscoverField describes one configuration key a client should store.
type AutodiscoverField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Env         string `json:"env"`
}

// AutodiscoverResponse is the bare object served at /api/autodiscover. It is
// unversioned by design: a client reads it before it knows which API version
// this instance speaks.
type AutodiscoverResponse struct {
	Project    string                 `json:"project"`
	APIVersion string                 `json:"api_version"`
	BaseURL    string                 `json:"base_url"`
	APIBase    string                 `json:"api_base"`
	Endpoints  AutodiscoverEndpoints  `json:"endpoints"`
	Auth       AutodiscoverAuth       `json:"auth"`
	Formats    []string               `json:"formats"`
	Pagination AutodiscoverPagination `json:"pagination"`
	Config     []AutodiscoverField    `json:"config"`
}

// AutodiscoverOptions configures the autodiscover handler.
type AutodiscoverOptions struct {
	// Project is the public project name.
	Project string
	// BaseURL is the configured public base URL. When empty the handler
	// answers with the scheme and host the client itself used, so it never
	// discloses an internal address the caller could not already see.
	BaseURL string
	// TokenPrefix is the prefix carried by issued API tokens.
	TokenPrefix string
	// EnvPrefix is the environment-variable prefix used by the CLI.
	EnvPrefix string
}

// Autodiscover serves the client bootstrap document.
type Autodiscover struct {
	opts AutodiscoverOptions
}

// NewAutodiscover builds the autodiscover handler.
func NewAutodiscover(opts AutodiscoverOptions) *Autodiscover {
	if opts.EnvPrefix == "" {
		opts.EnvPrefix = "CASHP"
	}
	return &Autodiscover{opts: opts}
}

// Response builds the autodiscover payload for one request.
func (a *Autodiscover) Response(r *http.Request) AutodiscoverResponse {
	base := strings.TrimRight(a.opts.BaseURL, "/")
	if base == "" {
		base = requestBaseURL(r)
	}
	env := strings.ToUpper(a.opts.EnvPrefix)
	return AutodiscoverResponse{
		Project:    a.opts.Project,
		APIVersion: Current().Version,
		BaseURL:    base,
		APIBase:    base + APIBasePath(),
		Endpoints: AutodiscoverEndpoints{
			Health:       base + APIPath("server", "healthz"),
			Ready:        base + APIPath("server", "readyz"),
			Version:      base + APIPath("server", "version"),
			OpenAPI:      base + APIPath("server", "swagger"),
			GraphQL:      base + APIPath("server", "graphql"),
			Autodiscover: base + UnversionedPath("autodiscover"),
		},
		Auth: AutodiscoverAuth{
			Methods:     []string{"bearer_token", "session_cookie"},
			Header:      "Authorization",
			Scheme:      "Bearer",
			TokenPrefix: a.opts.TokenPrefix,
		},
		Formats: []string{"application/json", "text/plain", "text/html"},
		Pagination: AutodiscoverPagination{
			DefaultLimit: DefaultPageSize,
			MaxLimit:     MaxPageSize,
			Params:       []string{"page", "limit", "sort", "order"},
		},
		Config: []AutodiscoverField{
			{
				Name:        "server",
				Type:        "string",
				Description: "Base URL of the server to talk to.",
				Required:    true,
				Default:     base,
				Env:         env + "_SERVER",
			},
			{
				Name:        "token",
				Type:        "string",
				Description: "API token used for authenticated requests.",
				Required:    true,
				Env:         env + "_TOKEN",
			},
			{
				Name:        "format",
				Type:        "string",
				Description: "Preferred response format: json, text, or html.",
				Default:     string(FormatJSON),
				Env:         env + "_FORMAT",
			},
			{
				Name:        "timeout",
				Type:        "duration",
				Description: "Per-request timeout, such as 30s.",
				Default:     "30s",
				Env:         env + "_TIMEOUT",
			},
		},
	}
}

// ServeHTTP renders the autodiscover payload in the negotiated format.
func (a *Autodiscover) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := a.Response(r)
	w.Header().Set("Cache-Control", "no-store")
	Write(w, r, http.StatusOK, Body{
		JSON:  resp,
		Title: resp.Project + " - Autodiscover",
	})
}

// requestBaseURL reconstructs the public origin from the request itself. The
// forwarded scheme is honoured because the real-IP middleware removes every
// X-Forwarded-* header that did not arrive from a trusted proxy.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(fwd, ",")[0]))
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}
