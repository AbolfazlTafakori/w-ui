package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// What every customer's page loads the same: its styles and its words.
//
// They were written into every page, 140 KB of a page of 170 to 320, sent
// whole on every visit and every reload, on networks where that is seconds.
// Now each is a file of its own, named for its content's hash: a browser
// fetches it once and keeps it for good, and a new binary's is a new name, so
// nothing stale is ever kept. Compressed once, here, not on every request.

const subAssetPrefix = "/sub-asset/"

//go:embed subpage.css
var subPageCSS []byte

// subAsset is one such file.
type subAsset struct {
	ctype      string
	body, gzip []byte
}

var subAssets sync.Map // path -> *subAsset

// registerSubAsset serves body under a name made from its hash, and says it.
func registerSubAsset(name, ext, ctype string, body []byte) string {
	sum := sha256.Sum256(body)
	path := subAssetPrefix + name + "." + hex.EncodeToString(sum[:6]) + ext
	if _, ok := subAssets.Load(path); !ok {
		var gz bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
		_, _ = zw.Write(body)
		_ = zw.Close()
		subAssets.Store(path, &subAsset{ctype: ctype, body: body, gzip: gz.Bytes()})
	}
	return path
}

// subCSSPath is the page's styles.
var subCSSPath = registerSubAsset("page", ".css", "text/css; charset=utf-8", subPageCSS)

// subStringsPaths are the page's words, by which of them a page needs
// (pageStrings): four files at most, made the first time each is asked for.
var subStringsPaths sync.Map // [2]bool -> string

func subStringsPath(l2tp, setup bool) string {
	key := [2]bool{l2tp, setup}
	if p, ok := subStringsPaths.Load(key); ok {
		return p.(string)
	}
	dict, _ := json.Marshal(pageStrings(l2tp, setup))
	p := registerSubAsset("words", ".js", "text/javascript; charset=utf-8",
		append(append([]byte("window.__wuiStrings="), dict...), ';', '\n'))
	subStringsPaths.Store(key, p)
	return p
}

// serveSubAsset answers /sub-asset/<name>.<hash>.<ext>, on the panel and the
// subscription service alike.
func serveSubAsset(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, subAssetPrefix) {
		return false
	}
	v, ok := subAssets.Load(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	a := v.(*subAsset)
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Vary", "Accept-Encoding")
	body := a.body
	if acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		body = a.gzip
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}
