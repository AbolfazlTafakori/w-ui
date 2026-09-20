package service

import (
	"context"
	"testing"
)

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

// A service on its own port is linked on that port: the port the panel
// was reached on is replaced, a host the operator typed keeps its own.
func TestLinkNamesTheServicePortNotThePanels(t *testing.T) {
	db := testDB(t)
	s := NewSubscriptions(db, nil, nil, quietLog())
	ctx := context.Background()
	if _, err := s.SaveSettings(ctx, SubSettings{Enabled: true, Path: "/subscribe/", Port: 44600, CertFile: "c", KeyFile: "k", UpdateHours: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LinkFor(ctx, "tok", "panel.example.com:2057")
	if err != nil || got != "https://panel.example.com:44600/subscribe/tok" {
		t.Fatalf("from the panel's host: %q %v", got, err)
	}
	if _, err := s.SaveSettings(ctx, SubSettings{Enabled: true, Path: "/subscribe/", Port: 44600, Host: "sub.example.com:8443", UpdateHours: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LinkFor(ctx, "tok", "panel.example.com:2057")
	if got != "http://sub.example.com:8443/subscribe/tok" {
		t.Fatalf("a host typed with its own port keeps it: %q", got)
	}
}

// The listen domain is the bare name in the Host setting, however it was
// typed; an address or nothing means the service answers to any host.
func TestListenDomain(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"sub.example.com":           "sub.example.com",
		"https://Sub.Example.com/":  "sub.example.com",
		"sub.example.com:8443":      "sub.example.com",
		"https://sub.example.com:1": "sub.example.com",
		"1.2.3.4":                   "",
		"[2001:db8::1]:443":         "",
	}
	for in, want := range cases {
		if got := ListenDomain(in); got != want {
			t.Errorf("ListenDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
