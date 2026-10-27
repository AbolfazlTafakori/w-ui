package api

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

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

// A wui-ws transport's private keys never leave the panel by the API; what
// else it has, the form shows.
func TestAnInterfaceKeepsItsTransportKeysFromTheAPI(t *testing.T) {
	in := model.Interface{Protocol: model.ProtocolWireGuard, Stealth: model.JSON(model.StealthParams{
		Transport: model.TransportWuiWS, Port: 8081, Host: "front.example", Path: "/abcdefghijklmn",
		Key: "current-private", PreviousKey: "previous-private", KeyAt: 7,
	})}
	out := redactKeys(in)
	got := out.Stealth.V
	if got.Key != "" || got.PreviousKey != "" {
		t.Fatalf("the transport's keys were left: %+v", got)
	}
	if got.Host != "front.example" || got.Path != "/abcdefghijklmn" || got.KeyAt != 7 {
		t.Fatalf("more than the keys went: %+v", got)
	}
	if in.Stealth.V.Key != "current-private" {
		t.Fatal("redacting changed the interface it was given")
	}
}
