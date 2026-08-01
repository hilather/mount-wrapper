// Package webui embeds the built TypeScript SPA for serving from the daemon.
//
// Build the SPA into this package's dist/ directory:
//
//	make web-build
//
// For local SPA development, run the Vite dev server (make web-dev) and point
// it at the Go API (proxy configured in web/vite.config.ts). The embedded
// assets are used in production when serve has web_enabled.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist holds the production SPA assets. A placeholder index.html is committed
// so `go test ./...` works before the first frontend build.
//
//go:embed all:dist
var dist embed.FS

// FS returns the SPA root (contents of dist/).
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

// Handler returns an http.Handler that serves the embedded SPA.
// Unknown paths fall back to index.html for client-side routing.
func Handler() (http.Handler, error) {
	sub, err := FS()
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try the requested path; if missing, serve index.html (SPA shell).
		path := r.URL.Path
		if path == "/" || path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Strip leading slash for fs lookup
		name := path
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
		if f, err := sub.Open(name); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Client route — serve index.html
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	}), nil
}
