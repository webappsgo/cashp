package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/api"
)

// fixture holds a route table and the dispatcher that serves it, mirroring the
// way the server hands its own middleware chain to the GraphQL handler.
type fixture struct {
	routes     []api.Route
	dispatcher http.Handler
	// lastPath records the path the executed field requested.
	lastPath string
	// lastQuery records the query string the executed field requested.
	lastQuery string
	// lastAuth records the Authorization header the executed field forwarded.
	lastAuth string
}

// newFixture builds the standard route table used by these tests.
func newFixture() *fixture {
	api.Configure(api.Config{})
	f := &fixture{}

	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]any{"status": "healthy", "version": "1.0.0"})
	})
	account := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastPath = r.URL.Path
		f.lastQuery = r.URL.RawQuery
		f.lastAuth = r.Header.Get("Authorization")
		api.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"data": map[string]any{"id": "42", "email": "user@example.com", "secretless": true},
		})
	})
	rejected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusNotFound, map[string]any{
			"ok":      false,
			"error":   "NOT_FOUND",
			"message": "The requested resource does not exist",
		})
	})
	created := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		api.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "data": body})
	})

	f.routes = []api.Route{
		{
			Method:   http.MethodGet,
			Pattern:  api.APIPath("server", "healthz"),
			Name:     "health",
			Summary:  "Server health status",
			Tags:     []string{"server"},
			Bare:     true,
			Response: []api.Field{{Name: "status", Type: "string"}},
			Handler:  health,
		},
		{
			Method:  http.MethodGet,
			Pattern: api.APIPath("accounts", "{id}"),
			Name:    "account",
			Summary: "One account",
			Tags:    []string{"accounts"},
			Params:  []api.Param{{Name: "expand", In: api.InQuery, Type: "string"}},
			Handler: account,
		},
		{
			Method:  http.MethodGet,
			Pattern: api.APIPath("missing"),
			Name:    "missing",
			Handler: rejected,
		},
		{
			Method:  http.MethodGet,
			Pattern: api.APIPath("private"),
			Name:    "private",
			Auth:    true,
			Handler: account,
		},
		{
			Method:  http.MethodPost,
			Pattern: api.APIPath("accounts"),
			Name:    "createAccount",
			Summary: "Create an account",
			Request: []api.Field{{Name: "email", Type: "string", Required: true}},
			Handler: created,
		},
		{
			Method:   http.MethodGet,
			Pattern:  "/server/docs/graphql",
			Name:     "docs",
			Internal: true,
			Handler:  health,
		},
	}

	mux := http.NewServeMux()
	for _, rt := range f.routes {
		if rt.Internal {
			continue
		}
		mux.Handle(rt.Method+" "+rt.Pattern, rt.Handler)
	}
	f.dispatcher = mux
	return f
}

// provider returns the fixture routes.
func (f *fixture) provider() []api.Route { return f.routes }

// handler builds a GraphQL handler over the fixture.
func (f *fixture) handler() *Handler {
	return NewHandler(f.provider, f.dispatcher, false)
}

// runQuery posts a query and decodes the GraphQL response.
func runQuery(t *testing.T, h *Handler, query string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, api.UnversionedPath("graphql"), strings.NewReader(`{"query":`+strconv.Quote(query)+`}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return out
}

// dataOf returns the data map of a GraphQL response.
func dataOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("the response carries no data object: %v", out)
	}
	return data
}

// errorsOf returns the errors array of a GraphQL response.
func errorsOf(t *testing.T, out map[string]any) []any {
	t.Helper()
	errs, ok := out["errors"].([]any)
	if !ok {
		t.Fatalf("the response carries no errors array: %v", out)
	}
	return errs
}

func TestSDLIsGeneratedFromTheMountedRoutes(t *testing.T) {
	sdl := newFixture().handler().SDL()

	for _, want := range []string{
		"scalar JSON",
		"type Query",
		"type Mutation",
		"health: JSON",
		"account(",
		"id: String!",
		"expand: String",
		"createAccount(",
		"email: String!",
	} {
		if !strings.Contains(sdl, want) {
			t.Fatalf("the schema is missing %q:\n%s", want, sdl)
		}
	}
	if strings.Contains(sdl, "docs") {
		t.Fatalf("an internal route leaked into the schema:\n%s", sdl)
	}
}

func TestQueryUnwrapsTheSuccessEnvelope(t *testing.T) {
	f := newFixture()
	out := runQuery(t, f.handler(), `{ account(id: "42") }`)

	if _, failed := out["errors"]; failed {
		t.Fatalf("unexpected errors: %v", out["errors"])
	}
	account := dataOf(t, out)["account"].(map[string]any)
	if account["id"] != "42" {
		t.Fatalf("account = %v; the envelope must be unwrapped", account)
	}
	if f.lastPath != api.APIPath("accounts", "42") {
		t.Fatalf("the executed path = %q", f.lastPath)
	}
}

func TestQueryKeepsABareDocumentIntact(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ health }`)

	health := dataOf(t, out)["health"].(map[string]any)
	if health["status"] != "healthy" {
		t.Fatalf("health = %v", health)
	}
}

func TestSubSelectionNarrowsTheDocument(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ health { status } }`)

	health := dataOf(t, out)["health"].(map[string]any)
	if len(health) != 1 || health["status"] != "healthy" {
		t.Fatalf("health = %v, only the selected field may be returned", health)
	}
}

func TestAliasBecomesTheResponseKey(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ state: health { status } }`)

	data := dataOf(t, out)
	if _, found := data["state"]; !found {
		t.Fatalf("data = %v, the alias must be the response key", data)
	}
	if _, found := data["health"]; found {
		t.Fatalf("data = %v, the field name must not also appear", data)
	}
}

func TestQueryArgumentsBindToPathAndQueryString(t *testing.T) {
	f := newFixture()
	runQuery(t, f.handler(), `{ account(id: "42", expand: "invoices") }`)

	if f.lastPath != api.APIPath("accounts", "42") {
		t.Fatalf("path = %q", f.lastPath)
	}
	values, err := url.ParseQuery(f.lastQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if values.Get("expand") != "invoices" {
		t.Fatalf("query = %q", f.lastQuery)
	}
}

func TestMissingRequiredPathArgumentIsReported(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ account }`)

	errs := errorsOf(t, out)
	first := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "required") {
		t.Fatalf("message = %v", first["message"])
	}
}

func TestRESTErrorEnvelopeBecomesAGraphQLError(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ missing }`)

	errs := errorsOf(t, out)
	first := errs[0].(map[string]any)
	if first["message"] != "The requested resource does not exist" {
		t.Fatalf("message = %v", first["message"])
	}
	ext := first["extensions"].(map[string]any)
	if ext["code"] != "NOT_FOUND" {
		t.Fatalf("extensions = %v", ext)
	}
	if ext["status"].(float64) != float64(http.StatusNotFound) {
		t.Fatalf("status = %v", ext["status"])
	}
	if dataOf(t, out)["missing"] != nil {
		t.Fatalf("a failed field must resolve to null: %v", out["data"])
	}
}

func TestUnknownFieldIsReportedWithoutFailingTheRequest(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `{ health nosuchfield }`)

	errs := errorsOf(t, out)
	first := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "nosuchfield") {
		t.Fatalf("message = %v", first["message"])
	}
	if dataOf(t, out)["health"] == nil {
		t.Fatal("the valid field must still resolve")
	}
}

func TestMutationRunsThroughTheDispatcher(t *testing.T) {
	out := runQuery(t, newFixture().handler(), `mutation { createAccount(email: "user@example.com") }`)

	if _, failed := out["errors"]; failed {
		t.Fatalf("unexpected errors: %v", out["errors"])
	}
	created := dataOf(t, out)["createAccount"].(map[string]any)
	if created["email"] != "user@example.com" {
		t.Fatalf("createAccount = %v", created)
	}
}

func TestMutationRequiresPOST(t *testing.T) {
	h := newFixture().handler()
	target := api.UnversionedPath("graphql") + "?" + url.Values{"query": {`mutation { createAccount(email: "a@b.c") }`}}.Encode()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestQueryOverGETIsAccepted(t *testing.T) {
	h := newFixture().handler()
	target := api.UnversionedPath("graphql") + "?" + url.Values{"query": {"{ health }"}}.Encode()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dataOf(t, out)["health"] == nil {
		t.Fatalf("data = %v", out["data"])
	}
}

func TestRawGraphQLBodyIsAccepted(t *testing.T) {
	h := newFixture().handler()

	r := httptest.NewRequest(http.MethodPost, api.UnversionedPath("graphql"), strings.NewReader("{ health }"))
	r.Header.Set("Content-Type", "application/graphql")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestEmptyQueryIsRejectedAtTheTransport(t *testing.T) {
	h := newFixture().handler()

	r := httptest.NewRequest(http.MethodPost, api.UnversionedPath("graphql"), strings.NewReader(`{"query":"  "}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAuthenticatedFieldForwardsTheCallerCredential(t *testing.T) {
	f := newFixture()

	r := httptest.NewRequest(http.MethodPost, api.UnversionedPath("graphql"), strings.NewReader(`{"query":"{ private }"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token-value")
	w := httptest.NewRecorder()
	f.handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if f.lastAuth != "Bearer token-value" {
		t.Fatalf("the credential was not forwarded to the executed route: %q", f.lastAuth)
	}
}

func TestAuthenticatedFieldRefusesWithoutADispatcher(t *testing.T) {
	f := newFixture()
	h := NewHandler(f.provider, nil, false)

	out := runQuery(t, h, `{ private }`)
	errs := errorsOf(t, out)
	first := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "authentication") {
		t.Fatalf("message = %v", first["message"])
	}
}

func TestARecursiveFieldIsRefused(t *testing.T) {
	f := newFixture()
	h := f.handler()

	r := httptest.NewRequest(http.MethodPost, api.UnversionedPath("graphql"), strings.NewReader(`{"query":"{ health }"}`))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), depthKey{}, true))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errs := errorsOf(t, out)
	first := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "may not resolve to the GraphQL endpoint") {
		t.Fatalf("message = %v", first["message"])
	}
}

func TestFragmentsAndSubscriptionsAreRejected(t *testing.T) {
	h := newFixture().handler()

	for _, query := range []string{
		"{ ...health }",
		"subscription { health }",
	} {
		out := runQuery(t, h, query)
		if _, failed := out["errors"]; !failed {
			t.Fatalf("%q was accepted", query)
		}
	}
}

func TestUIHandlerServesEveryFormat(t *testing.T) {
	h := newFixture().handler()
	endpoint := api.UnversionedPath("graphql")
	ui := h.UIHandler(endpoint)

	htmlReq := httptest.NewRequest(http.MethodGet, "/server/docs/graphql", nil)
	htmlReq.Header.Set("Accept", "text/html")
	htmlReq.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, htmlReq)
	body := w.Body.String()
	if !strings.HasPrefix(body, "<!DOCTYPE html>") {
		t.Fatalf("the explorer did not render a page: %.60s", body)
	}
	if !strings.Contains(body, "method=\"get\"") {
		t.Fatal("the explorer form must be a safe GET so it needs no CSRF token")
	}
	if !strings.Contains(body, endpoint) {
		t.Fatal("the explorer must name the GraphQL endpoint")
	}

	textReq := httptest.NewRequest(http.MethodGet, "/server/docs/graphql", nil)
	textReq.Header.Set("Accept", "text/plain")
	textReq.Header.Set("User-Agent", "Mozilla/5.0")
	w = httptest.NewRecorder()
	ui.ServeHTTP(w, textReq)
	if !strings.Contains(w.Body.String(), "type Query") {
		t.Fatalf("the text response must be the schema: %s", w.Body.String())
	}

	jsonReq := httptest.NewRequest(http.MethodGet, "/server/docs/graphql", nil)
	jsonReq.Header.Set("Accept", "application/json")
	jsonReq.Header.Set("User-Agent", "Mozilla/5.0")
	w = httptest.NewRecorder()
	ui.ServeHTTP(w, jsonReq)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["endpoint"] != endpoint || out["sdl"] == "" {
		t.Fatalf("json response = %v", out)
	}
}

func TestExplorerRunsASubmittedQuery(t *testing.T) {
	h := newFixture().handler()
	target := "/server/docs/graphql?" + url.Values{"query": {"{ health }"}}.Encode()

	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	h.UIHandler(api.UnversionedPath("graphql")).ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "Result") {
		t.Fatal("the explorer did not render a result section")
	}
	if !strings.Contains(body, "healthy") {
		t.Fatalf("the explorer did not run the query: %s", body)
	}
}
