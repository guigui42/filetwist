// Package web serves the filetwist HTMX interface: the upload page, the
// streaming multipart intake, the polled job fragments, downloads, health, and
// the optional local diagnostics endpoint.
package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// StaticFS returns the embedded static asset tree rooted at the asset
// directory.
func StaticFS() (fs.FS, error) {
	return fs.Sub(staticFS, "static")
}

func parseTemplates(functions template.FuncMap) (*template.Template, error) {
	return template.New("filetwist").Funcs(functions).ParseFS(templateFS, "templates/*.html")
}
