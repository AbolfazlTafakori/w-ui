// Package web serves the compiled frontend from inside the binary.
//
// Embedding it is what keeps deployment to a single file: there is no asset
// directory to copy alongside the binary and no way for the UI and the API to
// drift out of step, because they ship as one artifact.
package web

import (
	"bytes"
	"embed"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built frontend, falling back to index.html so that client
// side routes survive a page reload.
//
// base is the URL prefix the panel is mounted at, "/" for none. It is written
// into the page's <base> tag once, here, rather than being baked into the
// build: the prefix is chosen at install time and the binary is the same one
// everywhere.
func Handler(base string) (http.Handler, error) {
	client, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	files := http.FS(client)
	server := http.FileServer(files)

	// Rewritten once at startup, not per request: it is the same bytes every
	// time, and index.html is the one file served on nearly every navigation.
	index, indexErr := readIndex(client, base)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if name == "index.html" {
			serveIndex(w, index, indexErr)
			return
		}

		f, err := client.Open(name)
		if err != nil {
			// Not a real file: this is a client-side route, so hand back the
			// app shell and let the router work out what to render.
			serveIndex(w, index, indexErr)
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

func serveIndex(w http.ResponseWriter, index []byte, err error) {
	if err != nil {
		http.Error(w, "frontend not built: run npm run build in web/", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(index)
}

// readIndex loads the app shell and points its <base> at where the panel is
// actually mounted.
//
// The tag is expected: it is in the source index.html so that the dev server
// resolves the same way the binary does. If it is somehow missing, one is put
// in rather than serving a page whose every asset URL would be wrong.
func readIndex(client fs.FS, base string) ([]byte, error) {
	data, err := fs.ReadFile(client, "index.html")
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = "/"
	}
	want := `<base href="` + html.EscapeString(base) + `" />`

	if i := bytes.Index(data, []byte("<base ")); i >= 0 {
		if j := bytes.IndexByte(data[i:], '>'); j >= 0 {
			return append(append(append([]byte{}, data[:i]...), want...), data[i+j+1:]...), nil
		}
	}
	if i := bytes.Index(data, []byte("<head>")); i >= 0 {
		at := i + len("<head>")
		return append(append(append([]byte{}, data[:at]...), "\n    "+want...), data[at:]...), nil
	}
	return data, nil
}

// MountAt puts the whole panel behind a URL prefix.
//
// Anything outside the prefix gets a plain 404 -- the same answer as a path
// that does not exist -- so a scanner sweeping the address learns nothing about
// whether a panel is here at all. That is the entire point of the prefix, and
// a helpful redirect or a distinctive error page would give it away.
func MountAt(base string, next http.Handler) http.Handler {
	if base == "" || base == "/" {
		return next
	}
	bare := strings.TrimSuffix(base, "/")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == bare:
			// Somebody typed the prefix without its trailing slash. They
			// already know the secret, so sending them one step on costs
			// nothing and saves a confusing 404.
			http.Redirect(w, r, base, http.StatusMovedPermanently)
		case strings.HasPrefix(p, base):
			inner := r.Clone(r.Context())
			// Keep the leading slash: the mux below is mounted at the root.
			inner.URL.Path = p[len(bare):]
			next.ServeHTTP(w, inner)
		default:
			http.NotFound(w, r)
		}
	})
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
