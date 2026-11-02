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

// A drift transport's private keys never leave the panel by the API; what
// else it has, the form shows.
func TestAnInterfaceKeepsItsTransportKeysFromTheAPI(t *testing.T) {
	in := model.Interface{Protocol: model.ProtocolWireGuard, Stealth: model.JSON(model.StealthParams{
		Transport: model.TransportDrift, Port: 8081, Host: "front.example", Path: "/abcdefghijklmn",
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

	// bare's the same; its cookie's name is no secret, and the form
	// shows it.
	plain := model.Interface{Protocol: model.ProtocolWireGuard, Stealth: model.JSON(model.StealthParams{
		Transport: model.TransportBare, Port: 8081, Host: "plain.example", Cookie: "sid", Key: "current-private", PreviousKey: "previous-private",
	})}
	if got := redactKeys(plain).Stealth.V; got.Key != "" || got.PreviousKey != "" || got.Cookie != "sid" {
		t.Fatalf("bare redacted: %+v", got)
	}

	// anchor's certificate keys go too, the tunnel's own and the panel's
	// copy; the certificates, which every visitor is shown, stay.
	direct := model.Interface{Protocol: model.ProtocolWireGuard, Stealth: model.JSON(model.StealthParams{
		Transport: model.TransportAnchor, Port: 443, Host: "edge.example", Key: "current-private",
		Cert: "own-cert", CertKey: "own-key", PanelCert: "panel-cert", PanelCertKey: "panel-key", CertSource: model.CertSourcePanel,
	})}
	got = redactKeys(direct).Stealth.V
	if got.Key != "" || got.CertKey != "" || got.PanelCertKey != "" {
		t.Fatalf("anchor's keys were left: %+v", got)
	}
	if got.Cert != "own-cert" || got.PanelCert != "panel-cert" || got.CertSource != model.CertSourcePanel {
		t.Fatalf("more than anchor's keys went: %+v", got)
	}
}
