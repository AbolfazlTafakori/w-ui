package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The server's own key material must not leave this process. Whoever holds it
// can present themselves as this VPN server to every customer on it, and a
// response body passes through browser memory, proxy logs and error reporting
// on its way to a page that never needed it.
func TestServerKeysNeverReachTheAPI(t *testing.T) {
	iface := model.Interface{
		Name:       "tun0",
		Protocol:   model.ProtocolOpenVPN,
		PrivateKey: "WIREGUARD-PRIVATE-KEY",
		PublicKey:  "WIREGUARD-PUBLIC-KEY",
		OpenVPN: model.JSON(model.OpenVPNParams{
			CACert:      "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----",
			ServerCert:  "-----BEGIN CERTIFICATE-----\nSERVER\n-----END CERTIFICATE-----",
			ServerKey:   "-----BEGIN PRIVATE KEY-----\nSERVER-SECRET\n-----END PRIVATE KEY-----",
			TLSCryptKey: "-----BEGIN OpenVPN Static key V1-----\nTLSCRYPT-SECRET",
		}),
	}

	encoded, err := json.Marshal(redactKeys(iface))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(encoded)

	for _, secret := range []string{
		"SERVER-SECRET",   // impersonate the server to every customer
		"TLSCRYPT-SECRET", // connect to, and fingerprint, the tunnel
		"WIREGUARD-PRIVATE-KEY",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("%s reached the API response:\n%s", secret, body)
		}
	}
}

func TestTheAuthorityStillReachesTheAPI(t *testing.T) {
	iface := model.Interface{
		OpenVPN: model.JSON(model.OpenVPNParams{
			CACert:    "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----",
			ServerKey: "secret",
		}),
	}
	out := redactKeys(iface)

	// The authority is public by design — every customer's own configuration
	// carries it — and the panel shows it. Redacting it would break the page
	// for no gain.
	if out.OpenVPN.V.CACert == "" {
		t.Error("the certificate authority was redacted; it is public by design")
	}
	// And the page still has to be able to say whether certificates exist.
	if !out.Configured {
		t.Error("a configured interface does not report itself as configured")
	}
}

func TestAnInterfaceWithoutCertificatesIsNotReportedAsConfigured(t *testing.T) {
	out := redactKeys(model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard})
	if out.Configured {
		t.Error("an interface with no certificates reported itself as configured")
	}
}

func TestRedactionDoesNotTouchTheStoredRow(t *testing.T) {
	iface := model.Interface{
		OpenVPN: model.JSON(model.OpenVPNParams{ServerKey: "secret"}),
	}
	_ = redactKeys(iface)

	// It takes a copy. Clearing the caller's own struct would erase the key
	// from whatever is about to be written back to the database.
	if iface.OpenVPN.V.ServerKey != "secret" {
		t.Error("redaction modified the interface it was given")
	}
}
