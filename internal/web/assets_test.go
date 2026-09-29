package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Pages are fetched when first opened, as their own files. A tab from before
// an update asks for the old build's files, which the updated panel does not
// have: that has to be a 404 the page can see, not the app shell with a 200
// that the browser then tries to run as a script. A page address is still
// answered with the app, for the router in it to render.
func TestAMissingBuildFileIsNotFound(t *testing.T) {
	if !Built() {
		t.Skip("the frontend is not built")
	}
	h, err := Handler("/")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := get("/assets/OverviewView-fromAnOlderBuild.js"); rec.Code != http.StatusNotFound {
		t.Errorf("a missing build file answered %d, want 404", rec.Code)
	}
	if rec := get("/clients"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<div id=\"app\"") {
		t.Errorf("a page address answered %d without the app", rec.Code)
	}

	// And a page file that is there is served as a script.
	entries, err := fs.ReadDir(dist, "dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			rec := get("/assets/" + e.Name())
			if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
				t.Errorf("%s answered %d as %q", e.Name(), rec.Code, rec.Header().Get("Content-Type"))
			}
			break
		}
	}
}
