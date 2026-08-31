package server

import (
	"net/http"

	"github.com/webappsgo/cashp/src/api"
	"github.com/webappsgo/cashp/src/graphql"
	"github.com/webappsgo/cashp/src/swagger"
)

// DocsOptions carries the metadata the generated API documents need. The
// documents themselves are generated from the routes this server mounted, so
// they cannot drift from what is served (AI.md PART 14).
type DocsOptions struct {
	// Title is the product name shown in the OpenAPI document and explorer.
	Title string
	// Description is the short product description.
	Description string
	// Version is the running build version.
	Version string
	// BaseURL is the absolute base of the served API.
	BaseURL string
	// Contact is the published contact name.
	Contact string
	// License is the licence name.
	License string
	// LicenseURL is the licence URL.
	LicenseURL string
}

// MountDocs registers the OpenAPI document, the GraphQL endpoint, and the two
// interactive explorers.
//
// Both unversioned API aliases mount the same handler instance as their
// versioned canonical route; neither is a redirect, so a POST to /api/graphql
// is executed rather than bounced (AI.md PART 14).
func (s *Server) MountDocs(opts DocsOptions) (*swagger.Generator, *graphql.Handler) {
	generator := swagger.NewGenerator(s.Routes, swagger.Info{
		Title:       opts.Title,
		Description: opts.Description,
		Version:     opts.Version,
		BaseURL:     opts.BaseURL,
		Contact:     opts.Contact,
		License:     opts.License,
		LicenseURL:  opts.LicenseURL,
	})

	// The GraphQL executor replays each field through the server's own
	// middleware chain, so it is resolved lazily: the chain is rebuilt
	// whenever a route is mounted, and this keeps the executor on the
	// current one instead of a snapshot taken here.
	dispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Handler().ServeHTTP(w, r)
	})
	gql := graphql.NewHandler(s.Routes, dispatcher, s.opts.Debug)

	specCanonical := api.APIPath("server", "swagger")
	specHandler := generator.SpecHandler()
	s.MountRoute(api.Route{
		Method:      http.MethodGet,
		Pattern:     specCanonical,
		Name:        "swagger",
		Summary:     "OpenAPI document",
		Description: "The OpenAPI " + swagger.OpenAPIVersion + " description of this API, generated from the mounted routes. JSON only.",
		Tags:        []string{"server"},
		Bare:        true,
		Handler:     specHandler,
	})
	s.MountAlias("GET "+api.UnversionedPath("swagger"), specCanonical, specHandler)

	gqlCanonical := api.APIPath("server", "graphql")
	s.MountRoute(api.Route{
		Method:      http.MethodPost,
		Pattern:     gqlCanonical,
		Name:        "graphql",
		Summary:     "GraphQL endpoint",
		Description: "Executes a GraphQL query or mutation. Every field resolves to the same handler its REST route uses.",
		Tags:        []string{"server"},
		Bare:        true,
		Handler:     gql,
	})
	s.MountAlias("POST "+api.UnversionedPath("graphql"), gqlCanonical, gql)

	s.MountRoute(api.Route{
		Method:   http.MethodGet,
		Pattern:  "/server/docs/swagger",
		Name:     "docs-swagger",
		Summary:  "Interactive API explorer",
		Tags:     []string{"server"},
		Internal: true,
		Handler:  generator.UIHandler(api.UnversionedPath("swagger")),
	})
	s.MountRoute(api.Route{
		Method:   http.MethodGet,
		Pattern:  "/server/docs/graphql",
		Name:     "docs-graphql",
		Summary:  "Interactive GraphQL explorer",
		Tags:     []string{"server"},
		Internal: true,
		Handler:  gql.UIHandler(api.UnversionedPath("graphql")),
	})

	return generator, gql
}
