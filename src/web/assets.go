package web

import (
	"io/fs"
)

// TemplatesFS returns the embedded template tree rooted at templates/, so other
// packages — the admin panel in particular — can parse the shared layout,
// partials and components instead of duplicating them.
func TemplatesFS() (fs.FS, error) {
	return fs.Sub(assets, "templates")
}

// StaticFS returns the embedded static asset tree rooted at static/. It is the
// same tree StaticHandler serves, exposed for packages that mount the assets
// under a different prefix.
func StaticFS() (fs.FS, error) {
	return fs.Sub(assets, "static")
}
