package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// Success is the envelope every successful action response uses
// (AI.md PART 14 § Standard Response Formats).
type Success struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

// Failure is the canonical error envelope. The HTTP status code carries the
// status, so it is deliberately absent from the body.
type Failure struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Pagination describes the slice of a collection carried by a page
// response. The default page size is 250 items.
type Pagination struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
	Pages int `json:"pages"`
}

// PageResponse is the shape returned by collection endpoints.
type PageResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// Body carries the per-format representations of one response. JSON is
// always required; Text and HTML are optional and are derived from the JSON
// value when a handler does not supply its own rendering.
type Body struct {
	JSON any
	Text string
	HTML string
	// Title is used as the page title when HTML is derived.
	Title string
}

// Paginate reads the page and limit query parameters, applying the default
// page size of 250 and clamping to MaxPageSize. Invalid values fall back to
// the defaults rather than failing the request.
func Paginate(r *http.Request) (page, limit int) {
	page = 1
	limit = DefaultPageSize
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	return page, limit
}

// NewPagination builds the pagination block for a collection response.
func NewPagination(page, limit, total int) Pagination {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if page <= 0 {
		page = 1
	}
	pages := 0
	if total > 0 {
		pages = (total + limit - 1) / limit
	}
	return Pagination{Page: page, Limit: limit, Total: total, Pages: pages}
}

// Write renders one response in the negotiated format, always ending with
// exactly one trailing newline.
func Write(w http.ResponseWriter, r *http.Request, status int, b Body) {
	switch Negotiate(r) {
	case FormatText:
		text := b.Text
		if text == "" {
			text = TextOf(b.JSON)
		}
		writeRaw(w, status, FormatText, ensureNewline(text))
	case FormatHTML:
		page := b.HTML
		if page == "" {
			page = derivedHTML(b.Title, TextOf(b.JSON))
		}
		writeRaw(w, status, FormatHTML, ensureNewline(page))
	default:
		writeRaw(w, status, FormatJSON, encodeJSON(b.JSON))
	}
}

// WriteJSON writes a JSON body regardless of negotiation. It is used by
// routes whose contract is JSON-only, such as the OpenAPI spec endpoint.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeRaw(w, status, FormatJSON, encodeJSON(v))
}

// WriteText writes a plain-text body regardless of negotiation.
func WriteText(w http.ResponseWriter, status int, text string) {
	writeRaw(w, status, FormatText, ensureNewline(text))
}

// WriteHTML writes an HTML body regardless of negotiation.
func WriteHTML(w http.ResponseWriter, status int, page string) {
	writeRaw(w, status, FormatHTML, ensureNewline(page))
}

// WriteSuccess writes the {ok:true,data:{...}} envelope for a create,
// update, or delete action.
func WriteSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	Write(w, r, status, Body{JSON: Success{OK: true, Data: data}, Title: "Result"})
}

// WriteItem writes a single item directly, without an envelope, as required
// for single-item reads.
func WriteItem(w http.ResponseWriter, r *http.Request, status int, item any) {
	Write(w, r, status, Body{JSON: item, Title: "Item"})
}

// WritePage writes a paginated collection response.
func WritePage(w http.ResponseWriter, r *http.Request, items any, total int) {
	page, limit := Paginate(r)
	Write(w, r, http.StatusOK, Body{
		JSON:  PageResponse{Data: items, Pagination: NewPagination(page, limit, total)},
		Title: "Results",
	})
}

// WriteError writes the canonical error envelope. The message and details
// come from the error package; the underlying wrapped error, stack traces,
// connection strings, internal addresses, and filesystem paths are never
// included, in any mode.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.From(err)
	status := e.HTTPStatus
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}
	message := e.Message
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}
	RecordErrorCode(r, e.Code)
	failure := Failure{OK: false, Error: e.Code, Message: message, Details: safeDetails(e.Details)}
	Write(w, r, status, Body{
		JSON:  failure,
		Text:  errorText(failure),
		HTML:  errorHTML(failure, status),
		Title: "Error",
	})
}

// safeDetails copies structured error context, dropping any key whose name
// marks it as sensitive so validation feedback can never carry a secret.
func safeDetails(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitiveKey(k) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sensitiveKeyParts marks detail keys that must never reach a client.
var sensitiveKeyParts = []string{"secret", "password", "token", "key", "dsn", "credential", "path", "host", "addr"}

// isSensitiveKey reports whether a detail key names sensitive data.
func isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(lk, part) {
			return true
		}
	}
	return false
}

// errorText renders the error envelope as plain text.
func errorText(f Failure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ok: false\n")
	fmt.Fprintf(&b, "error: %s\n", f.Error)
	fmt.Fprintf(&b, "message: %s\n", f.Message)
	for _, k := range sortedKeys(f.Details) {
		fmt.Fprintf(&b, "details.%s: %v\n", k, f.Details[k])
	}
	return b.String()
}

// errorHTML renders a minimal, self-contained error page.
func errorHTML(f Failure, status int) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n  <head>\n")
	b.WriteString("    <meta charset=\"UTF-8\">\n")
	b.WriteString("    <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	fmt.Fprintf(&b, "    <title>%d %s</title>\n", status, html.EscapeString(http.StatusText(status)))
	b.WriteString("  </head>\n  <body>\n    <main class=\"container\">\n")
	fmt.Fprintf(&b, "      <h1>%d %s</h1>\n", status, html.EscapeString(http.StatusText(status)))
	fmt.Fprintf(&b, "      <p class=\"error-code\"><code>%s</code></p>\n", html.EscapeString(f.Error))
	fmt.Fprintf(&b, "      <p class=\"error-message\">%s</p>\n", html.EscapeString(f.Message))
	b.WriteString("    </main>\n  </body>\n</html>\n")
	return b.String()
}

// derivedHTML wraps flattened text output in a minimal HTML document for
// handlers that do not supply their own template.
func derivedHTML(title, text string) string {
	if title == "" {
		title = "cashp"
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n  <head>\n")
	b.WriteString("    <meta charset=\"UTF-8\">\n")
	b.WriteString("    <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	fmt.Fprintf(&b, "    <title>%s</title>\n", html.EscapeString(title))
	b.WriteString("  </head>\n  <body>\n    <main class=\"container\">\n")
	fmt.Fprintf(&b, "      <h1>%s</h1>\n", html.EscapeString(title))
	fmt.Fprintf(&b, "      <pre class=\"code-content\">%s</pre>\n", html.EscapeString(text))
	b.WriteString("    </main>\n  </body>\n</html>\n")
	return b.String()
}

// writeRaw sets the content type and status, then writes the body exactly
// as given.
func writeRaw(w http.ResponseWriter, status int, f Format, body string) {
	w.Header().Set("Content-Type", f.ContentType())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// encodeJSON marshals a value with the mandated 2-space indent and single
// trailing newline. Marshal failures degrade to the canonical INTERNAL
// error envelope rather than emitting a partial document.
func encodeJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fallback, _ := json.MarshalIndent(Failure{
			OK:      false,
			Error:   apperr.CodeInternal,
			Message: http.StatusText(http.StatusInternalServerError),
		}, "", "  ")
		return string(fallback) + "\n"
	}
	return string(data) + "\n"
}

// ensureNewline guarantees a body ends with exactly one newline.
func ensureNewline(s string) string {
	return strings.TrimRight(s, "\n") + "\n"
}

// TextOf renders any JSON-encodable value as dot-notation "key: value"
// lines, preserving the field order of the encoded document.
func TextOf(v any) string {
	if v == nil {
		return ""
	}
	if tr, ok := v.(TextRenderer); ok {
		return tr.RenderText()
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var b strings.Builder
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSON(dec, "", &b); err != nil {
		return ""
	}
	return b.String()
}

// TextRenderer is implemented by payloads that define their own canonical
// plain-text rendering, such as the health response.
type TextRenderer interface {
	RenderText() string
}

// walkJSON converts a JSON token stream into dot-notation text lines.
func walkJSON(dec *json.Decoder, prefix string, b *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		writeLine(b, prefix, scalarText(tok))
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string)
			child := key
			if prefix != "" {
				child = prefix + "." + key
			}
			if err := walkJSON(dec, child, b); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		return walkArray(dec, prefix, b)
	}
	return nil
}

// walkArray renders an array: scalar elements join on one line, structured
// elements are indexed.
func walkArray(dec *json.Decoder, prefix string, b *strings.Builder) error {
	var scalars []string
	index := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			sub := json.NewDecoder(bytes.NewReader(trimmed))
			sub.UseNumber()
			if err := walkJSON(sub, fmt.Sprintf("%s.%d", prefix, index), b); err != nil {
				return err
			}
		} else {
			sub := json.NewDecoder(bytes.NewReader(trimmed))
			sub.UseNumber()
			tok, err := sub.Token()
			if err != nil {
				return err
			}
			scalars = append(scalars, scalarText(tok))
		}
		index++
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if index == 0 || len(scalars) > 0 {
		writeLine(b, prefix, strings.Join(scalars, ", "))
	}
	return nil
}

// scalarText formats a JSON scalar token for text output.
func scalarText(tok json.Token) string {
	switch t := tok.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// writeLine appends one "key: value" line to the text output.
func writeLine(b *strings.Builder, key, value string) {
	if key == "" {
		b.WriteString(value)
		b.WriteString("\n")
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

// sortedKeys returns map keys in a stable order for deterministic output.
func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
