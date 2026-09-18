package api

import (
	"embed"
	"net/http"
	"strings"
)

// Persian on the subscription page.
//
// The page sets its type in the face each template chose -- the system
// sans, a monospace for the terminal looks, a serif for the paper ones --
// and none of those carry Persian: the browser falls back glyph by glyph
// to whatever it finds, which on a phone is a spaced-out, mismatched
// script. Vazirmatn's Arabic subset is carried in the binary and declared
// for that range of characters only, so Persian is set in it while the
// Latin and the figures stay in the template's own face.
//
//go:embed fonts/vazirmatn-400.woff2 fonts/vazirmatn-700.woff2
var subFonts embed.FS

const subFontPrefix = "/sub-font/"

// serveSubFont answers /sub-font/<file>.woff2 from the embedded files, on
// the panel and on the subscription service alike, cached for a year:
// the file changes only with the binary.
func serveSubFont(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, subFontPrefix) {
		return false
	}
	name := strings.TrimPrefix(r.URL.Path, subFontPrefix)
	if strings.ContainsAny(name, "/\\") || !strings.HasSuffix(name, ".woff2") {
		http.NotFound(w, r)
		return true
	}
	b, err := subFonts.ReadFile("fonts/" + name)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	h := w.Header()
	h.Set("Content-Type", "font/woff2")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
	return true
}
