package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The prefix is only worth anything if everything outside it is indistinguishable
// from an address with no panel on it.
func TestMountAt(t *testing.T) {
	var sawPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	h := MountAt("/s3cret/", inner)

	cases := []struct {
		path     string
		want     int
		wantPath string
	}{
		// A scanner sweeping the address finds nothing, including at the
		// places a panel is usually found.
		{"/", http.StatusNotFound, ""},
		{"/login", http.StatusNotFound, ""},
		{"/api/auth/login", http.StatusNotFound, ""},
		{"/assets/index.js", http.StatusNotFound, ""},

		// Nor does a near miss confirm the prefix exists.
		{"/s3cre/", http.StatusNotFound, ""},
		{"/s3cretx/", http.StatusNotFound, ""},

		// Somebody who has the prefix gets the panel, and the handler below
		// sees paths as though it were mounted at the root.
		{"/s3cret/", http.StatusOK, "/"},
		{"/s3cret/clients", http.StatusOK, "/clients"},
		{"/s3cret/api/overview", http.StatusOK, "/api/overview"},

		// The prefix without its trailing slash is a typo by somebody who
		// already knows it, so it is worth one redirect.
		{"/s3cret", http.StatusMovedPermanently, ""},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			sawPath = ""
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
			}
			if sawPath != tc.wantPath {
				t.Errorf("GET %s reached the panel as %q, want %q", tc.path, sawPath, tc.wantPath)
			}
		})
	}

	// A 404 from outside the prefix has to look like any other 404. A body
	// naming the panel would answer the only question the scanner had.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := rec.Body.String(); strings.Contains(strings.ToLower(body), "wui") ||
		strings.Contains(strings.ToLower(body), "panel") {
		t.Errorf("the 404 body names the panel: %q", body)
	}
}

// With no prefix configured the panel stays exactly where it was.
func TestMountAtRootIsUnchanged(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	for _, base := range []string{"", "/"} {
		rec := httptest.NewRecorder()
		MountAt(base, inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
		if rec.Code != http.StatusTeapot {
			t.Errorf("MountAt(%q) intercepted a request; got %d", base, rec.Code)
		}
	}
}

// Every asset URL and every API call in the page is resolved against this tag,
// so getting it wrong breaks the whole panel rather than one link.
func TestReadIndexRewritesTheBaseTag(t *testing.T) {
	cases := []struct {
		name, in, base, want string
	}{
		{
			name: "the tag the build produces",
			in:   `<html><head><base href="/" />` + "\n" + `<title>x</title></head></html>`,
			base: "/s3cret/", want: `<base href="/s3cret/" />`,
		},
		{
			name: "a root mount still gets an explicit tag",
			in:   `<html><head><base href="/" /></head></html>`,
			base: "/", want: `<base href="/" />`,
		},
		{
			// Not expected, but a page with no base tag would resolve its
			// assets against whatever route it was loaded on.
			name: "a page that lost its tag has one put back",
			in:   `<html><head><title>x</title></head></html>`,
			base: "/s3cret/", want: `<base href="/s3cret/" />`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readIndex(fakeFS{"index.html": tc.in}, tc.base)
			if err != nil {
				t.Fatalf("readIndex: %v", err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Fatalf("readIndex gave %q, want it to contain %q", got, tc.want)
			}
			if n := strings.Count(string(got), "<base "); n != 1 {
				t.Errorf("found %d base tags, want exactly 1: %q", n, got)
			}
		})
	}
}

// A base path that arrived with markup in it must not become markup.
func TestReadIndexEscapesTheBase(t *testing.T) {
	got, err := readIndex(fakeFS{"index.html": `<html><head><base href="/" /></head></html>`},
		`/"><script>x</script>/`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "<script>") {
		t.Fatalf("the base path was written into the page as markup: %q", got)
	}
}
