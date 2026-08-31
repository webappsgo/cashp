// Package urlutil builds correctly encoded API URLs. Every URL that embeds
// user input MUST be built here — never with fmt.Sprintf — per AI.md
// PART 33 "URL Encoding".
//
// This package belongs in src/common/urlutil once that shared tree exists;
// it lives under src/client so both the CLI and the agent have exactly one
// implementation today.
package urlutil

import (
	"net/url"
	"strings"
)

// BuildAPIURL joins baseURL with a path template, substituting {name}
// placeholders from pathParams (path-escaped) and appending queryParams
// (query-escaped). An unparseable baseURL yields an empty string, which
// callers treat as a configuration error.
func BuildAPIURL(baseURL, path string, pathParams map[string]string, queryParams map[string]string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}

	encodedPath := path
	for key, value := range pathParams {
		encodedPath = strings.ReplaceAll(encodedPath, "{"+key+"}", url.PathEscape(value))
	}

	if !strings.HasPrefix(encodedPath, "/") {
		encodedPath = "/" + encodedPath
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + encodedPath

	if len(queryParams) > 0 {
		q := u.Query()
		for key, value := range queryParams {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	return u.String()
}

// EncodePathSegment encodes a single path segment such as a username, org
// slug, resource id or filename.
func EncodePathSegment(segment string) string {
	return url.PathEscape(segment)
}

// EncodeQueryValue encodes a single query parameter value such as a search
// term or filter.
func EncodeQueryValue(value string) string {
	return url.QueryEscape(value)
}

// BuildQueryString encodes a parameter map into a sorted query string.
func BuildQueryString(params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return values.Encode()
}

// NormalizeBase trims a trailing slash from a server base URL so path
// joining never produces a double slash.
func NormalizeBase(raw string) string {
	return strings.TrimSuffix(strings.TrimSpace(raw), "/")
}
