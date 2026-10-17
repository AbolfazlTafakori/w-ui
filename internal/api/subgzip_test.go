package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abolfazl/w-ui/internal/database"
)

// What the subscription service sends is compressed for a caller that takes
// it, and is exactly what it was once uncompressed; for a caller that does
// not, it is sent as it always was.
func TestTheSubscriptionIsSentCompressedToWhoTakesIt(t *testing.T) {
	s, _ := revealServer(t)
	s.subMiss = newSubMisses()
	for k, v := range map[string]string{"sub.enabled": "true", "sub.path": "/sub/"} {
		if err := database.PutSetting(s.db, k, v); err != nil {
			t.Fatal(err)
		}
	}
	router := s.SubscriptionRouter(http.NotFoundHandler())
	ask := func(accept, encoding string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+revealToken, nil)
		req.Header.Set("Accept", accept)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, accept := range []string{"text/html", "*/*"} { // the page, and the files
		plain := ask(accept, "")
		if plain.Code != http.StatusOK || plain.Header().Get("Content-Encoding") != "" {
			t.Fatalf("%s without gzip: %d %q", accept, plain.Code, plain.Header().Get("Content-Encoding"))
		}
		packed := ask(accept, "gzip, deflate, br")
		if packed.Header().Get("Content-Encoding") != "gzip" || packed.Header().Get("Content-Length") != "" {
			t.Fatalf("%s with gzip: %q", accept, packed.Header().Get("Content-Encoding"))
		}
		zr, err := gzip.NewReader(packed.Body)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(zr)
		// The page carries a per-request mark for its copy button; the rest
		// is the same.
		if accept == "*/*" && !bytes.Equal(got, plain.Body.Bytes()) {
			t.Fatalf("%s: uncompressed differs from what is sent plain", accept)
		}
		if accept == "text/html" && (len(got) < plain.Body.Len()-200 || len(got) > plain.Body.Len()+200 || packed.Body.Len() >= plain.Body.Len()) {
			t.Fatalf("page: %d plain, %d uncompressed, %d sent", plain.Body.Len(), len(got), packed.Body.Len())
		}
	}
	if rec := ask("*/*", "gzip;q=0"); rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed for a caller that refuses gzip")
	}
}
