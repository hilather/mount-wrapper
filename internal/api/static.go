package api

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/hilather/mount-wrapper/internal/webui"
)

// spaHandler serves the embedded SPA with client-route fallback to index.html.
// When token is non-empty, injects window.__MOUNT_WRAPPER_TOKEN__ into index.html.
func spaHandler(token string) (http.Handler, error) {
	sub, err := webui.FS()
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never serve SPA for /api/* or /metrics (should already be routed).
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/metrics" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found", "path": r.URL.Path})
			return
		}

		// Normalize path.
		upath := r.URL.Path
		if upath == "" {
			upath = "/"
		}

		// Known SPA client routes → index.html
		if upath == "/" || upath == "/index.html" || upath == "/settings" || upath == "/settings.html" {
			serveIndex(w, r, sub, token)
			return
		}

		// Strip leading slash for embed FS lookup.
		name := strings.TrimPrefix(path.Clean(upath), "/")
		if name == "" || name == "." {
			serveIndex(w, r, sub, token)
			return
		}
		// Path escape guard.
		if strings.Contains(name, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad path"})
			return
		}

		if f, err := sub.Open(name); err == nil {
			_ = f.Close()
			// Let FileServer handle content-type / caching for assets.
			fileServer.ServeHTTP(w, r)
			return
		}

		// Client-side route fallback.
		serveIndex(w, r, sub, token)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS, token string) {
	f, err := sub.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read index", http.StatusInternalServerError)
		return
	}

	if token != "" {
		// JSON-encode token so injection is safe inside a script tag.
		raw, _ := json.Marshal(token)
		snippet := []byte("<script>window.__MOUNT_WRAPPER_TOKEN__=" + string(raw) + ";</script>")
		if bytes.Contains(data, []byte("</head>")) {
			data = bytes.Replace(data, []byte("</head>"), append(snippet, []byte("</head>")...), 1)
		} else {
			data = append(snippet, data...)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
