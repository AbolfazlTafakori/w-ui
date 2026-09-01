// Package web serves the compiled frontend from inside the binary.
//
// Embedding it is what keeps deployment to a single file: there is no asset
// directory to copy alongside the binary and no way for the UI and the API to
// drift out of step, because they ship as one artifact.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built frontend, falling back to index.html so that client
// side routes survive a page reload.
func Handler() (http.Handler, error) {
	client, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	files := http.FS(client)
	server := http.FileServer(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		f, err := client.Open(name)
		if err != nil {
			// Not a real file: this is a client-side route, so hand back the
			// app shell and let the router work out what to render.
			serveIndex(w, r, client)
			return
		}
		f.Close()

		// Hashed asset filenames change whenever their content does, so they
		// can be cached indefinitely; index.html must not be.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		server.ServeHTTP(w, r)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, client fs.FS) {
	data, err := fs.ReadFile(client, "index.html")
	if err != nil {
		http.Error(w, "frontend not built: run npm run build in web/", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// Built reports whether a real frontend was embedded, as opposed to the
// placeholder that keeps the Go build working before the first npm build.
func Built() bool {
	entries, err := fs.ReadDir(dist, "dist")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "assets" {
			return true
		}
	}
	return false
}
