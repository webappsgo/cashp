package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
)

// maxBodyBytes caps every request body this package parses. A larger body is refused
// before it is buffered, so a hostile client cannot exhaust memory on an auth endpoint.
const maxBodyBytes = 1 << 20

// decodeJSON reads a JSON request body into dst. Unknown fields are rejected so a
// misspelled field can never be silently ignored and treated as absent.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) *apperr.Error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apperr.New(apperr.CodeValidation, http.StatusRequestEntityTooLarge,
				"That request body is too large")
		}
		return ErrValidation("body", "Send a valid JSON request body")
	}
	// A second value in the stream would mean the client sent more than one document.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return ErrValidation("body", "Send a single JSON request body")
	}
	return nil
}

// parseForm reads an HTML form body under the same size cap.
func parseForm(w http.ResponseWriter, r *http.Request) *apperr.Error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		return ErrValidation("body", "Send a valid form submission")
	}
	return nil
}

// isJSONRequest reports whether the client sent a JSON body.
func isJSONRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(ct)), "application/json")
}

// bind fills dst from either a JSON body or an HTML form, so every API endpoint accepts
// a plain form post and the site keeps working with JavaScript disabled.
func bind(w http.ResponseWriter, r *http.Request, dst any, form func(*http.Request)) *apperr.Error {
	if isJSONRequest(r) {
		return decodeJSON(w, r, dst)
	}
	if aerr := parseForm(w, r); aerr != nil {
		return aerr
	}
	form(r)
	return nil
}

// formBool reads a checkbox value.
func formBool(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.PostFormValue(name))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// formInt reads an integer field, returning zero when absent or unparseable.
func formInt(r *http.Request, name string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(name)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// formList reads a repeated field plus a comma-separated fallback, which is what a
// no-JavaScript multi-select and a plain text input respectively produce.
func formList(r *http.Request, name string) []string {
	values := r.PostForm[name]
	out := make([]string, 0, len(values))
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// pathInt reads a numeric path segment.
func pathInt(r *http.Request, name string) int64 {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// ok writes a JSON success envelope.
func ok(w http.ResponseWriter, r *http.Request, data any) {
	api.WriteSuccess(w, r, http.StatusOK, data)
}

// created writes a JSON success envelope with a 201 status.
func created(w http.ResponseWriter, r *http.Request, data any) {
	api.WriteSuccess(w, r, http.StatusCreated, data)
}

// fail writes the canonical error envelope. The underlying cause never leaves the
// process; only the caller-safe code and message are serialized.
func fail(w http.ResponseWriter, r *http.Request, e *apperr.Error) {
	api.WriteError(w, r, e)
}

// messageOnly is the response body for operations with nothing to return.
type messageOnly struct {
	Message string `json:"message"`
}
