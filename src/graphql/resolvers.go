package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/api"
)

// depthKey marks a request that is already being executed on behalf of a
// GraphQL field, which stops a field that resolves to the GraphQL route from
// recursing into itself.
type depthKey struct{}

// forwardedHeaders are the request headers copied onto an executed field.
// Only headers that carry the caller's identity or negotiation intent are
// forwarded; hop-by-hop and body-describing headers are rebuilt.
var forwardedHeaders = []string{
	"Authorization",
	"Cookie",
	"X-Api-Key",
	"X-CSRF-Token",
	"X-Request-ID",
	"Accept-Language",
	"User-Agent",
}

// execute runs every top-level selection of an operation and collects the
// results into the data map.
func (h *Handler) execute(r *http.Request, op operation) (map[string]any, []gqlError) {
	if r.Context().Value(depthKey{}) != nil {
		return nil, []gqlError{{Message: "a GraphQL field may not resolve to the GraphQL endpoint"}}
	}

	routes := h.provider()
	queries, mutations := schemaFields(routes)
	available := queries
	if op.kind == opMutation {
		available = mutations
	}
	index := make(map[string]field, len(available))
	for _, f := range available {
		index[f.name] = f
	}

	data := map[string]any{}
	var errs []gqlError
	for _, sel := range op.selections {
		f, ok := index[sel.name]
		if !ok {
			data[sel.key()] = nil
			errs = append(errs, gqlError{
				Message: fmt.Sprintf("the %s type has no field named %q", op.kind, sel.name),
				Path:    []string{sel.key()},
			})
			continue
		}
		value, err := h.resolve(r, f, sel)
		if err != nil {
			data[sel.key()] = nil
			errs = append(errs, *err)
			continue
		}
		data[sel.key()] = filterSelection(value, sel.subs)
	}
	return data, errs
}

// resolve executes one field by replaying its route through the server's own
// handler chain and decoding the response it produced.
func (h *Handler) resolve(r *http.Request, f field, sel selection) (any, *gqlError) {
	if f.route.Auth && h.dispatcher == nil {
		return nil, &gqlError{
			Message: "this field requires authentication and cannot be executed on this endpoint",
			Path:    []string{sel.key()},
		}
	}

	req, err := h.buildRequest(r, f, sel)
	if err != nil {
		return nil, &gqlError{Message: err.Error(), Path: []string{sel.key()}}
	}

	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	handler := h.dispatcher
	if handler == nil {
		handler = f.route.Handler
	}
	if handler == nil {
		return nil, &gqlError{Message: "this field has no handler mounted", Path: []string{sel.key()}}
	}
	handler.ServeHTTP(rec, req)

	return decodeResult(rec, f, sel)
}

// buildRequest turns a field selection into the HTTP request its route
// expects: path wildcards become path segments, query parameters become the
// query string, and everything else becomes the JSON body.
func (h *Handler) buildRequest(r *http.Request, f field, sel selection) (*http.Request, error) {
	path, query, body, err := bindArguments(f, sel.args)
	if err != nil {
		return nil, err
	}

	method := f.route.Method
	if method == "" {
		method = http.MethodGet
	}
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	var payload io.Reader
	var raw []byte
	if len(body) > 0 && method != http.MethodGet && method != http.MethodHead {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("the arguments of %q could not be encoded", sel.name)
		}
		payload = bytes.NewReader(raw)
	}

	ctx := context.WithValue(r.Context(), depthKey{}, true)
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, fmt.Errorf("the arguments of %q do not form a valid request", sel.name)
	}
	req.Host = r.Host
	req.RemoteAddr = r.RemoteAddr
	req.TLS = r.TLS
	for _, name := range forwardedHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	req.Header.Set("Accept", api.FormatJSON.ContentType())
	if raw != nil {
		req.Header.Set("Content-Type", api.FormatJSON.ContentType())
		req.ContentLength = int64(len(raw))
	}
	return req, nil
}

// bindArguments distributes a field's arguments over the path, the query
// string, and the request body according to the route's declarations.
func bindArguments(f field, args map[string]any) (string, url.Values, map[string]any, error) {
	placement := map[string]api.ParamIn{}
	for _, p := range f.route.Params {
		placement[p.Name] = p.In
	}
	bodyFields := map[string]bool{}
	for _, bf := range f.route.Request {
		bodyFields[bf.Name] = true
	}

	used := map[string]bool{}
	segments := strings.Split(patternPath(f.route.Pattern), "/")
	for i, seg := range segments {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.Trim(seg, "{}"), "...")
		if name == "" {
			continue
		}
		value, ok := args[name]
		if !ok {
			return "", nil, nil, fmt.Errorf("argument %q is required", name)
		}
		text := scalarString(value)
		if text == "" || strings.Contains(text, "/") {
			return "", nil, nil, fmt.Errorf("argument %q is not a valid path value", name)
		}
		segments[i] = url.PathEscape(text)
		used[name] = true
	}

	query := url.Values{}
	body := map[string]any{}
	for _, name := range sortedArgNames(args) {
		if used[name] {
			continue
		}
		value := args[name]
		switch {
		case placement[name] == api.InQuery:
			query.Set(name, scalarString(value))
		case bodyFields[name]:
			body[name] = value
		case placement[name] == api.InHeader:
			continue
		default:
			query.Set(name, scalarString(value))
		}
	}
	return strings.Join(segments, "/"), query, body, nil
}

// patternPath strips a leading method from a route pattern.
func patternPath(pattern string) string {
	_, path := api.SplitPattern(pattern)
	if path == "" {
		return pattern
	}
	return path
}

// sortedArgNames returns argument names in a stable order so a request built
// from the same arguments is always byte-identical.
func sortedArgNames(args map[string]any) []string {
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scalarString renders a scalar argument as the text a URL carries.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// decodeResult turns the recorded REST response into the field's value,
// unwrapping the success envelope and converting an error envelope into a
// GraphQL error. The message comes from the REST layer, which has already
// stripped anything that must not reach a client.
func decodeResult(rec *recorder, f field, sel selection) (any, *gqlError) {
	var decoded any
	if rec.body.Len() > 0 {
		if err := json.Unmarshal(rec.body.Bytes(), &decoded); err != nil {
			return nil, &gqlError{
				Message: "the resolved endpoint did not return a JSON document",
				Path:    []string{sel.key()},
			}
		}
	}

	obj, isObject := decoded.(map[string]any)
	if isObject {
		if ok, present := obj["ok"].(bool); present && !ok {
			gerr := &gqlError{Message: "the request was rejected", Path: []string{sel.key()}}
			if msg, found := obj["message"].(string); found && msg != "" {
				gerr.Message = msg
			}
			ext := map[string]any{"status": rec.status}
			if code, found := obj["error"].(string); found && code != "" {
				ext["code"] = code
			}
			if details, found := obj["details"]; found {
				ext["details"] = details
			}
			gerr.Extra = ext
			return nil, gerr
		}
		if !f.route.Bare {
			if data, present := obj["data"]; present {
				decoded = data
			}
		}
	}

	if rec.status >= http.StatusBadRequest {
		return nil, &gqlError{
			Message: http.StatusText(rec.status),
			Path:    []string{sel.key()},
			Extra:   map[string]any{"status": rec.status},
		}
	}
	return decoded, nil
}

// filterSelection narrows a resolved value to the fields the query asked for.
// A field with no sub-selection returns the whole document, which is what the
// JSON scalar means.
func filterSelection(value any, subs []selection) any {
	if len(subs) == 0 {
		return value
	}
	switch t := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for _, sub := range subs {
			out[sub.key()] = filterSelection(t[sub.name], sub.subs)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, filterSelection(item, subs))
		}
		return out
	default:
		return value
	}
}

// recorder buffers a response written by an executed route so its body can be
// decoded instead of being sent to the client.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

// Header returns the buffered header map.
func (rec *recorder) Header() http.Header { return rec.header }

// WriteHeader records the status of the buffered response.
func (rec *recorder) WriteHeader(status int) {
	if rec.wrote {
		return
	}
	rec.wrote = true
	rec.status = status
}

// Write buffers response bytes.
func (rec *recorder) Write(p []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.body.Write(p)
}
