package service

import "testing"

func TestWithPort(t *testing.T) {
	cases := map[string]string{
		"example.com":              "example.com:2096",
		"https://example.com":      "https://example.com:2096",
		"example.com:8443":         "example.com:8443",
		"https://example.com:8443": "https://example.com:8443",
		"1.2.3.4":                  "1.2.3.4:2096",
		"2001:db8::1":              "[2001:db8::1]:2096",
		"[2001:db8::1]:443":        "[2001:db8::1]:443",
	}
	for in, want := range cases {
		if got := withPort(in, 2096); got != want {
			t.Errorf("withPort(%q) = %q, want %q", in, got, want)
		}
	}
}
