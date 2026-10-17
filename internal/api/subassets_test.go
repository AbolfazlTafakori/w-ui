package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The page's styles and words are files of their own: kept by the browser
// for good under a name that is their content's, compressed for who takes it.
func TestThePagesStylesAndWordsAreFilesKeptForGood(t *testing.T) {
	words := subStringsPath(true, true)
	if words != subStringsPath(true, true) || words == subStringsPath(false, false) {
		t.Fatal("the same words are not the same file, or other words are")
	}
	for _, path := range []string{subCSSPath, words} {
		for _, enc := range []string{"", "gzip"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if enc != "" {
				req.Header.Set("Accept-Encoding", enc)
			}
			rec := httptest.NewRecorder()
			if !serveSubAsset(rec, req) || rec.Code != http.StatusOK {
				t.Fatalf("%s: %d", path, rec.Code)
			}
			if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
				t.Fatalf("%s is cached as %q", path, cc)
			}
			body := rec.Body.Bytes()
			if enc != "" {
				if rec.Header().Get("Content-Encoding") != "gzip" {
					t.Fatalf("%s not compressed", path)
				}
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(zr)
			}
			a, _ := subAssets.Load(path)
			if !bytes.Equal(body, a.(*subAsset).body) {
				t.Fatalf("%s (%s) is not the file", path, enc)
			}
		}
	}
	if !strings.HasSuffix(subCSSPath, ".css") || !bytes.Contains(subPageCSS, []byte(".setup-card")) || bytes.Contains(subPageCSS, []byte("{{")) {
		t.Fatal("the styles file is not the page's styles, whole")
	}
	rec := httptest.NewRecorder()
	if !serveSubAsset(rec, httptest.NewRequest(http.MethodGet, subAssetPrefix+"page.000000000000.css", nil)) || rec.Code != http.StatusNotFound {
		t.Fatalf("a name that is no file: %d", rec.Code)
	}
}
