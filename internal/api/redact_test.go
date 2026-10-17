package api

import "testing"

// A log keeps no secret a path carries: a customer's token, the panel's
// hidden path. The rest of a path is as it was.
func TestALogKeepsNoSecretOfAPath(t *testing.T) {
	for in, want := range map[string]string{
		"/subscribe/x8IZuECsuG9byG2-qf1VVAEOh6EVGpvS": "/subscribe/x8IZ…",
		"/sub/u2jytsuacvm7fpnk":                       "/sub/u2jy…",
		"/kG1MooGH1LDkLOooGj/api/clients/42":          "/kG1M…/api/clients/42",
		"/api/clients/42/rotate-keys":                 "/api/clients/42/rotate-keys",
		"/sub-asset/page.40a5ec8e603b.css":            "/sub-asset/page.40a5ec8e603b.css",
		"/favicon.ico":                                "/favicon.ico",
		"/":                                           "/",
	} {
		if got := redactPath(in); got != want {
			t.Errorf("%s logged as %s, want %s", in, got, want)
		}
	}
}
