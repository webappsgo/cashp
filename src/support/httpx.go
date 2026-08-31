package support

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// maxFormBytes bounds a support form submission. Attachments arrive on their
// own multipart endpoint and are bounded separately by the configured size.
const maxFormBytes = 1 << 20

// envelope is the success half of the API contract.
type envelope struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

// identity resolves the caller. When the host application supplied no resolver
// the caller is treated as a guest, which is the safe default: a guest can read
// nothing that belongs to anyone.
func (s *Service) identity(r *http.Request) Identity {
	if s.opts.Identity == nil {
		return Identity{RemoteKey: remoteKey(r)}
	}
	id := s.opts.Identity(r)
	if id.RemoteKey == "" {
		id.RemoteKey = remoteKey(r)
	}
	return id
}

// remoteKey derives a rate-limit key for an unauthenticated caller. Only the
// address is used, never a header the client controls.
func remoteKey(r *http.Request) string {
	host := r.RemoteAddr
	if idx := strings.LastIndexByte(host, ':'); idx > 0 {
		host = host[:idx]
	}
	return strings.Trim(host, "[]")
}

// writeJSON writes a successful API response in the shared envelope.
func (s *Service) writeJSON(w http.ResponseWriter, status int, data any) {
	body, err := json.Marshal(envelope{OK: true, Data: data})
	if err != nil {
		s.writeError(w, nil, errors.New(errors.CodeInternal, 500, "Response could not be encoded"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.logger().Debug("support response write failed")
	}
}

// writeError renders an error for the API. The response carries only a code and
// a sentence written for a human: no stack trace, no query, no path, and no
// detail about the storage layer ever reaches the client.
func (s *Service) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := errors.From(err)
	if e.HTTPStatus >= 500 {
		s.logger().Error("support request failed",
			"error_code", e.Code,
			"path", requestPath(r))
	}
	if writeErr := e.WriteJSON(w); writeErr != nil {
		s.logger().Debug("support error write failed")
	}
}

// requestPath returns the request's path for logging, tolerating a nil request.
func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.Path
}

// wantsJSON reports whether the caller asked for the API rather than a page.
// Every handler serves both, so the whole subsystem works with JavaScript
// switched off and the same handler answers a fetch call.
func (s *Service) wantsJSON(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

// CSRFToken mints the token the support forms carry.
func (s *Service) CSRFToken(id Identity) string {
	if len(s.opts.CSRFSecret) == 0 || id.SessionID == "" {
		return ""
	}
	return security.NewCSRFToken(s.opts.CSRFSecret, id.SessionID)
}

// checkCSRF rejects a state-changing request that did not carry a valid token
// for this session. It runs before any handler body reads a form value.
func (s *Service) checkCSRF(r *http.Request, id Identity) error {
	if len(s.opts.CSRFSecret) == 0 {
		return errors.New(errors.CodeInternal, 500, "This request could not be verified")
	}
	if id.SessionID == "" {
		return errors.New(errors.CodeForbidden, 403, "This request could not be verified")
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.PostFormValue("csrf_token")
	}
	if !security.ValidateCSRFToken(s.opts.CSRFSecret, id.SessionID, token) {
		return errors.New(errors.CodeForbidden, 403, "This request could not be verified")
	}
	return nil
}

// readForm parses a bounded form body. Both HTML forms and JSON bodies land
// here so a handler never has to care which one it is serving.
func (s *Service) readForm(r *http.Request) error {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(nil, r.Body, maxFormBytes)
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var fields map[string]string
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			return errors.New(errors.CodeBadRequest, 400, "That request body could not be read")
		}
		if r.Form == nil {
			r.Form = map[string][]string{}
		}
		for k, v := range fields {
			r.Form.Set(k, v)
		}
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return errors.New(errors.CodeBadRequest, 400, "That form could not be read")
	}
	return nil
}

// formValue reads one submitted field from either source.
func formValue(r *http.Request, name string) string {
	if r.Form != nil {
		if v, ok := r.Form[name]; ok && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// formBool reads a checkbox or JSON boolean.
func formBool(r *http.Request, name string) bool {
	switch strings.ToLower(formValue(r, name)) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// formInt reads an integer field, returning the fallback when it is absent or
// unparseable rather than failing the request.
func formInt(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(formValue(r, name)))
	if err != nil {
		return fallback
	}
	return n
}

// queryInt reads an integer query parameter.
func queryInt(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil {
		return fallback
	}
	return n
}

// pageParams reads the shared pagination parameters, clamped to the limits the
// panel uses everywhere.
func pageParams(r *http.Request) (int, int) {
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := queryInt(r, "limit", DefaultPageLimit)
	if limit < 1 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	return page, limit
}

// pathTail returns the segment of the request path after a prefix, with any
// trailing slash removed. It never returns a segment containing a slash, so a
// crafted path cannot be read as an identifier plus an extra route.
func pathTail(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// pathParts splits the remainder of a path into its segments.
func pathParts(path, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

// requirePOST rejects a state-changing request that arrived by any other
// method, so nothing that changes data can be triggered by a link or an image.
func requirePOST(r *http.Request) error {
	if r.Method != http.MethodPost {
		return errors.New(errors.CodeMethodNotAllowed, 405, "Use POST for this action")
	}
	return nil
}

// redirect sends a browser back to a support page after a form submission.
func (s *Service) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, s.opts.BasePath+path, http.StatusSeeOther)
}
