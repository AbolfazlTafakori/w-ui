package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Compressing what the subscription service sends.
//
// A customer's page carries their files' QR codes, the styles and the icons:
// 170 to 320 KB, sent as it was to every phone that opened it, on networks
// where that is seconds. Compressed it is half that and less; a subscription
// of text links shrinks further. Only for a caller that says it takes gzip --
// every browser, and the HTTP clients the apps are built on -- and never for
// what is already compressed or what must arrive as it is written.

var gzipWriters = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// subGzip compresses the subscription answer to r when it can be.
func subGzip(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if !acceptsGzip(r) || r.Method == http.MethodHead || r.URL.Query().Get("view") == "events" {
		// The page's event stream is read as it is written, a line at a time.
		return w, func() {}
	}
	gw := &gzipResponse{ResponseWriter: w}
	return gw, gw.finish
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") && strings.ReplaceAll(q, " ", "") != "q=0" {
			return true
		}
	}
	return false
}

// gzipResponse compresses a body once its headers say it should be: not one
// already compressed (an archive, an image), not one with no body.
type gzipResponse struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func (g *gzipResponse) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		h := g.Header()
		h.Add("Vary", "Accept-Encoding")
		ct := h.Get("Content-Type")
		if code != http.StatusNoContent && code != http.StatusNotModified && h.Get("Content-Encoding") == "" &&
			!strings.HasPrefix(ct, "image/") && ct != "application/zip" && ct != "application/gzip" {
			h.Set("Content-Encoding", "gzip")
			h.Del("Content-Length")
			g.gz = gzipWriters.Get().(*gzip.Writer)
			g.gz.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponse) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipResponse) finish() {
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(io.Discard)
		gzipWriters.Put(g.gz)
		g.gz = nil
	}
}
