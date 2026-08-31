package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// TestDecodeJSONHappyRejectsUnknownFieldsAndMultipleDocuments covers the
// request-body decoder used by every JSON API handler: a well-formed body
// decodes cleanly, an unrecognized field is rejected rather than silently
// dropped, and a body containing more than one JSON document is rejected.
func TestDecodeJSONHappyRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}

	t.Run("happy path", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"a"}`))
		var dst body
		if aerr := decodeJSON(w, r, &dst); aerr != nil {
			t.Fatalf("decodeJSON: %v", aerr)
		}
		if dst.Name != "a" {
			t.Errorf("Name = %q, want a", dst.Name)
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"a","bogus":1}`))
		var dst body
		if aerr := decodeJSON(w, r, &dst); aerr == nil {
			t.Error("decodeJSON with an unknown field succeeded, want a validation error")
		}
	})

	t.Run("multiple documents rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"a"}{"name":"b"}`))
		var dst body
		if aerr := decodeJSON(w, r, &dst); aerr == nil {
			t.Error("decodeJSON with two JSON documents succeeded, want a validation error")
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{not json`))
		var dst body
		if aerr := decodeJSON(w, r, &dst); aerr == nil {
			t.Error("decodeJSON with malformed JSON succeeded, want a validation error")
		}
	})

	t.Run("empty body rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(``))
		var dst body
		if aerr := decodeJSON(w, r, &dst); aerr == nil {
			t.Error("decodeJSON with an empty body succeeded, want a validation error")
		}
	})

	t.Run("oversized body rejected", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), maxBodyBytes+1024)
		payload, err := json.Marshal(map[string]string{"name": string(big)})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(payload))
		var dst body
		aerr := decodeJSON(w, r, &dst)
		if aerr == nil {
			t.Fatal("decodeJSON with an oversized body succeeded, want a request-too-large error")
		}
		if aerr.HTTPStatus != http.StatusRequestEntityTooLarge {
			t.Errorf("HTTPStatus = %d, want %d", aerr.HTTPStatus, http.StatusRequestEntityTooLarge)
		}
	})
}

// TestParseFormAndBind exercises the form-fallback path bind() takes for a
// non-JSON request, matching the no-JS-first form-POST contract.
func TestParseFormAndBind(t *testing.T) {
	type body struct{ Name string }
	var dst body

	form := url.Values{"name": {"from-form"}}
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	if aerr := bind(w, r, &dst, func(r *http.Request) {
		dst.Name = r.PostFormValue("name")
	}); aerr != nil {
		t.Fatalf("bind (form): %v", aerr)
	}
	if dst.Name != "from-form" {
		t.Errorf("Name = %q, want from-form", dst.Name)
	}
}

func TestBindJSONPath(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}
	var dst body
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"from-json"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if aerr := bind(w, r, &dst, func(r *http.Request) {
		t.Fatal("bind called the form fallback for a JSON request")
	}); aerr != nil {
		t.Fatalf("bind (json): %v", aerr)
	}
	if dst.Name != "from-json" {
		t.Errorf("Name = %q, want from-json", dst.Name)
	}
}

func TestIsJSONRequest(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"  APPLICATION/JSON  ", true},
		{"application/x-www-form-urlencoded", false},
		{"", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		if c.contentType != "" {
			r.Header.Set("Content-Type", c.contentType)
		}
		if got := isJSONRequest(r); got != c.want {
			t.Errorf("isJSONRequest(%q) = %v, want %v", c.contentType, got, c.want)
		}
	}
}

func TestFormHelpers(t *testing.T) {
	form := url.Values{
		"flag_true":  {"yes"},
		"flag_on":    {"on"},
		"flag_false": {"nope"},
		"count":      {"42"},
		"count_bad":  {"not-a-number"},
		"tags":       {"a,b", "c"},
	}
	r := httptest.NewRequest(http.MethodPost, "/x?", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	if !formBool(r, "flag_true") {
		t.Error(`formBool("flag_true") = false, want true`)
	}
	if !formBool(r, "flag_on") {
		t.Error(`formBool("flag_on") = false, want true`)
	}
	if formBool(r, "flag_false") {
		t.Error(`formBool("flag_false") = true, want false`)
	}
	if formBool(r, "absent") {
		t.Error(`formBool("absent") = true, want false`)
	}

	if got := formInt(r, "count"); got != 42 {
		t.Errorf(`formInt("count") = %d, want 42`, got)
	}
	if got := formInt(r, "count_bad"); got != 0 {
		t.Errorf(`formInt("count_bad") = %d, want 0`, got)
	}
	if got := formInt(r, "absent"); got != 0 {
		t.Errorf(`formInt("absent") = %d, want 0`, got)
	}

	got := formList(r, "tags")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("formList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("formList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := formList(r, "absent"); len(got) != 0 {
		t.Errorf("formList(absent) = %v, want empty", got)
	}
}

func TestPathInt(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x/42", nil)
	r.SetPathValue("id", "42")
	if got := pathInt(r, "id"); got != 42 {
		t.Errorf("pathInt = %d, want 42", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/x/bogus", nil)
	r2.SetPathValue("id", "bogus")
	if got := pathInt(r2, "id"); got != 0 {
		t.Errorf("pathInt(non-numeric) = %d, want 0", got)
	}
}

// TestOkCreatedFailWriteEnvelopes checks the three response helpers write the
// canonical success/error envelopes documented in api-rules.md.
func TestOkCreatedFailWriteEnvelopes(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Header.Set("Accept", "application/json")
		ok(w, r, map[string]string{"hello": "world"})
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		var body struct {
			OK   bool           `json:"ok"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !body.OK || body.Data["hello"] != "world" {
			t.Errorf("body = %+v, want ok=true data.hello=world", body)
		}
	})

	t.Run("created", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.Header.Set("Accept", "application/json")
		created(w, r, map[string]string{"a": "b"})
		if w.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201", w.Code)
		}
	})

	t.Run("fail", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Header.Set("Accept", "application/json")
		fail(w, r, apperr.New(apperr.CodeValidation, http.StatusBadRequest, "bad input"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
		var body struct {
			OK      bool   `json:"ok"`
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if body.OK {
			t.Error("fail() wrote ok=true, want false")
		}
		if body.Error != apperr.CodeValidation || body.Message != "bad input" {
			t.Errorf("body = %+v, want error=%s message=%q", body, apperr.CodeValidation, "bad input")
		}
	})
}
