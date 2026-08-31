package swagger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/api"
)

// testRoutes is the route table the generated document is built from.
func testRoutes() []api.Route {
	canonical := api.APIPath("server", "healthz")
	return []api.Route{
		{
			Method:      http.MethodGet,
			Pattern:     canonical,
			Name:        "health",
			Summary:     "Server health status",
			Description: "Public health document.",
			Tags:        []string{"server"},
			Bare:        true,
			Response:    []api.Field{{Name: "status", Type: "string", Description: "Overall status."}},
			Handler:     http.NotFoundHandler(),
		},
		{
			Method:    http.MethodGet,
			Pattern:   api.UnversionedPath("healthz"),
			Name:      "healthAlias",
			Summary:   "Server health status",
			Tags:      []string{"server"},
			Bare:      true,
			Alias:     true,
			Canonical: canonical,
			Handler:   http.NotFoundHandler(),
		},
		{
			Method:  http.MethodGet,
			Pattern: api.APIPath("accounts", "{id}"),
			Name:    "account",
			Summary: "One account",
			Tags:    []string{"accounts"},
			Auth:    true,
			Params:  []api.Param{{Name: "expand", In: api.InQuery, Type: "string", Description: "Related records to include."}},
			Handler: http.NotFoundHandler(),
		},
		{
			Method:  http.MethodPost,
			Pattern: api.APIPath("accounts"),
			Name:    "createAccount",
			Summary: "Create an account",
			Tags:    []string{"accounts"},
			Auth:    true,
			Request: []api.Field{{Name: "email", Type: "string", Required: true}},
			Handler: http.NotFoundHandler(),
		},
		{
			Method:   http.MethodGet,
			Pattern:  "/server/docs/swagger",
			Name:     "docs",
			Internal: true,
			Handler:  http.NotFoundHandler(),
		},
	}
}

// newTestGenerator builds a generator over the fixture routes.
func newTestGenerator() *Generator {
	api.Configure(api.Config{})
	return NewGenerator(testRoutes, Info{
		Title:       "cashp",
		Description: "Hosting control panel",
		Version:     "1.0.0",
		BaseURL:     "https://panel.example.com/",
		Contact:     "cashp",
		License:     "MIT",
		LicenseURL:  "https://opensource.org/licenses/MIT",
	})
}

func TestDocumentIsGeneratedFromTheMountedRoutes(t *testing.T) {
	doc := newTestGenerator().Document()

	if doc["openapi"] != OpenAPIVersion {
		t.Fatalf("openapi = %v, want %s", doc["openapi"], OpenAPIVersion)
	}
	info := doc["info"].(map[string]any)
	if info["title"] != "cashp" || info["version"] != "1.0.0" {
		t.Fatalf("info = %v", info)
	}
	if info["license"].(map[string]any)["name"] != "MIT" {
		t.Fatalf("license = %v", info["license"])
	}
	servers := doc["servers"].([]any)
	if servers[0].(map[string]any)["url"] != "https://panel.example.com" {
		t.Fatalf("server url = %v, the trailing slash must be trimmed", servers[0])
	}

	paths := doc["paths"].(map[string]any)
	for _, want := range []string{
		api.APIPath("server", "healthz"),
		api.UnversionedPath("healthz"),
		api.APIPath("accounts", "{id}"),
		api.APIPath("accounts"),
	} {
		if _, found := paths[want]; !found {
			t.Fatalf("the document is missing %s", want)
		}
	}
	if _, found := paths["/server/docs/swagger"]; found {
		t.Fatal("an internal route must never be documented")
	}
}

func TestAliasOperationSaysItIsNotARedirect(t *testing.T) {
	doc := newTestGenerator().Document()
	paths := doc["paths"].(map[string]any)
	op := paths[api.UnversionedPath("healthz")].(map[string]any)["get"].(map[string]any)

	description, _ := op["description"].(string)
	if !strings.Contains(description, "Alias of "+api.APIPath("server", "healthz")) {
		t.Fatalf("description = %q", description)
	}
	if !strings.Contains(description, "not a redirect") {
		t.Fatalf("description = %q", description)
	}
}

func TestOperationCarriesParametersRequestBodyAndSecurity(t *testing.T) {
	doc := newTestGenerator().Document()
	paths := doc["paths"].(map[string]any)

	get := paths[api.APIPath("accounts", "{id}")].(map[string]any)["get"].(map[string]any)
	if get["operationId"] != "account" {
		t.Fatalf("operationId = %v", get["operationId"])
	}
	if get["security"] == nil {
		t.Fatal("an authenticated route must declare security")
	}
	params := get["parameters"].([]any)
	var sawPathID, sawQuery bool
	for _, raw := range params {
		p := raw.(map[string]any)
		if p["name"] == "id" && p["in"] == "path" && p["required"] == true {
			sawPathID = true
		}
		if p["name"] == "expand" && p["in"] == "query" {
			sawQuery = true
		}
	}
	if !sawPathID {
		t.Fatalf("the path wildcard is not documented: %v", params)
	}
	if !sawQuery {
		t.Fatalf("the query parameter is not documented: %v", params)
	}

	post := paths[api.APIPath("accounts")].(map[string]any)["post"].(map[string]any)
	body := post["requestBody"].(map[string]any)
	if body["required"] != true {
		t.Fatalf("requestBody = %v", body)
	}
	schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if _, found := schema["properties"].(map[string]any)["email"]; !found {
		t.Fatalf("schema = %v", schema)
	}
}

func TestBareRouteResponseIsNotEnveloped(t *testing.T) {
	doc := newTestGenerator().Document()
	paths := doc["paths"].(map[string]any)
	op := paths[api.APIPath("server", "healthz")].(map[string]any)["get"].(map[string]any)

	responses := op["responses"].(map[string]any)
	schema := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, enveloped := props["ok"]; enveloped {
		t.Fatalf("a bare route must not be documented with the envelope: %v", props)
	}
	if _, found := props["status"]; !found {
		t.Fatalf("the documented fields are missing: %v", props)
	}
}

func TestEnvelopedRouteDocumentsTheSuccessEnvelope(t *testing.T) {
	doc := newTestGenerator().Document()
	paths := doc["paths"].(map[string]any)
	op := paths[api.APIPath("accounts", "{id}")].(map[string]any)["get"].(map[string]any)

	responses := op["responses"].(map[string]any)
	schema := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, found := props["ok"]; !found {
		t.Fatalf("the success envelope is missing: %v", props)
	}
	if _, found := responses["401"]; !found {
		t.Fatalf("an authenticated route must document 401: %v", responses)
	}
}

func TestTagListCoversTheDocumentedRoutes(t *testing.T) {
	doc := newTestGenerator().Document()
	tags := doc["tags"].([]any)

	names := map[string]bool{}
	for _, raw := range tags {
		names[raw.(map[string]any)["name"].(string)] = true
	}
	if !names["server"] || !names["accounts"] {
		t.Fatalf("tags = %v", names)
	}
}

func TestSpecHandlerAlwaysAnswersJSON(t *testing.T) {
	g := newTestGenerator()
	h := g.SpecHandler()

	for _, accept := range []string{"application/json", "text/plain", "text/html"} {
		r := httptest.NewRequest(http.MethodGet, api.APIPath("server", "swagger"), nil)
		r.Header.Set("Accept", accept)
		r.Header.Set("User-Agent", "Mozilla/5.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("Accept %q status = %d", accept, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Accept %q gave Content-Type %q; the document is JSON only", accept, ct)
		}
		var doc map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("Accept %q: decode: %v", accept, err)
		}
	}
}

func TestUIHandlerServesEveryFormat(t *testing.T) {
	g := newTestGenerator()
	h := g.UIHandler(api.UnversionedPath("swagger"))
	specPath := api.UnversionedPath("swagger")

	html := httptest.NewRequest(http.MethodGet, "/server/docs/swagger", nil)
	html.Header.Set("Accept", "text/html")
	html.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, html)
	if w.Code != http.StatusOK {
		t.Fatalf("html status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "<!DOCTYPE html>") {
		t.Fatalf("the explorer did not render a page: %.60s", body)
	}
	if !strings.Contains(body, specPath) {
		t.Fatal("the explorer must link the OpenAPI document")
	}
	if !strings.Contains(body, "<details>") {
		t.Fatal("the explorer must work without JavaScript")
	}
	if strings.Contains(body, "<code>/server/docs/swagger</code>") {
		t.Fatal("an internal route leaked into the explorer")
	}

	text := httptest.NewRequest(http.MethodGet, "/server/docs/swagger", nil)
	text.Header.Set("Accept", "text/plain")
	text.Header.Set("User-Agent", "Mozilla/5.0")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, text)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("text Content-Type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), api.APIPath("accounts")) {
		t.Fatalf("the text listing is missing a route: %s", w.Body.String())
	}

	jsonReq := httptest.NewRequest(http.MethodGet, "/server/docs/swagger", nil)
	jsonReq.Header.Set("Accept", "application/json")
	jsonReq.Header.Set("User-Agent", "Mozilla/5.0")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, jsonReq)
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc["openapi"] != OpenAPIVersion {
		t.Fatalf("openapi = %v", doc["openapi"])
	}
}

func TestMethodClassCoversTheCommonMethods(t *testing.T) {
	seen := map[string]bool{}
	for _, method := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, "ANY",
	} {
		class := methodClass(method)
		if class == "" {
			t.Fatalf("methodClass(%q) is empty", method)
		}
		seen[class] = true
	}
	if len(seen) < 2 {
		t.Fatalf("every method rendered with the same badge class: %v", seen)
	}
}
