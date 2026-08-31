package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// staticPrefix is the URL prefix the static assets are mounted under.
const staticPrefix = "/static/"

// StaticHandler serves the embedded CSS, JavaScript, icons and manifest. It
// accepts both the prefixed and unprefixed form so it can be mounted with or
// without http.StripPrefix.
func (r *Renderer) StaticHandler() http.Handler {
	return r.static
}

// staticFileHandler wraps a file server with immutable caching for fingerprinted
// assets and a conservative default for everything else.
func staticFileHandler(files fs.FS) http.Handler {
	server := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed\n", http.StatusMethodNotAllowed)
			return
		}

		trimmed := req.URL.Path
		if strings.HasPrefix(trimmed, staticPrefix) {
			trimmed = strings.TrimPrefix(trimmed, "/static")
		}
		if !strings.HasPrefix(trimmed, "/") {
			trimmed = "/" + trimmed
		}

		// Directory listings expose the asset tree and serve no purpose here.
		if strings.HasSuffix(trimmed, "/") {
			http.NotFound(w, req)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", cacheControlFor(trimmed))

		scoped := req.Clone(req.Context())
		scoped.URL.Path = trimmed
		server.ServeHTTP(w, scoped)
	})
}

// cacheControlFor picks a caching policy for a static path. The service worker
// must never be cached long, or an update can never roll out.
func cacheControlFor(path string) string {
	switch {
	case strings.HasSuffix(path, "/sw.js"), strings.HasSuffix(path, "/manifest.json"):
		return "no-cache"
	case strings.HasPrefix(path, "/icons/"), strings.HasPrefix(path, "/fonts/"):
		return "public, max-age=604800"
	default:
		return "public, max-age=3600"
	}
}
