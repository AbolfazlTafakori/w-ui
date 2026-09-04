package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A node is handed peers and nothing else.
//
// The whole reason the plan stays central is that one allowance is spent across
// every server: a node holding its own copy would enforce its own share and cut
// a customer off at a third of what they bought. So the payload must not carry
// one, and this is what stops a field being added later without the thought.
func TestNodeStateCarriesNoPlan(t *testing.T) {
	body, err := json.Marshal(NodeState{
		Interface: NodeInterface{OriginID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard},
		Clients: []NodeClient{{
			OriginID: 7, Enabled: true, RateBitsPerSec: 1_000_000,
			Accounts: []NodeAccount{{OriginID: 11, DeviceName: "Phone", IP: "10.0.0.2"}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)

	// Everything that decides whether a customer may connect stays on the panel
	// that sold the plan. A node is told the answer, not the inputs.
	for _, forbidden := range []string{
		"quota", "Quota",
		"expire", "Expire", "expiresAt",
		"resetCycle", "usedBytes",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the node payload carries %q; the plan must stay central:\n%s", forbidden, got)
		}
	}

	// And the one thing it does carry.
	if !strings.Contains(got, `"enabled":true`) {
		t.Errorf("the payload does not say whether the customer may connect:\n%s", got)
	}
}

// A node may be rented from somebody else. It has no business learning who the
// customers are, so nothing identifying is sent and the row it stores is named
// after an id.
func TestNodeStateCarriesNoCustomerIdentity(t *testing.T) {
	body, _ := json.Marshal(NodeClient{
		OriginID: 7,
		Enabled:  true,
		Accounts: []NodeAccount{{OriginID: 11, DeviceName: "Phone"}},
	})
	got := string(body)

	for _, forbidden := range []string{"name\":", "note", "group", "subToken"} {
		// deviceName is the exception and is deliberate: the customer's own
		// configuration file is named after it, and a node that did not know it
		// could not produce the file.
		if strings.Contains(got, forbidden) && !strings.Contains(got, "deviceName") {
			t.Errorf("the node payload carries %q:\n%s", forbidden, got)
		}
	}
}

// The credentials do have to go, and it is worth being explicit about which:
// the node terminates the tunnel, so it needs the server key and every peer's
// key. A customer's configuration names the server's public key, and a node
// generating its own would hand every customer a file for a server that is not
// there.
func TestNodeStateCarriesTheCredentialsTheNodeMustHave(t *testing.T) {
	body, _ := json.Marshal(NodeState{
		Interface: NodeInterface{
			OriginID: 1, Name: "wg0",
			PrivateKey: "server-private", PublicKey: "server-public",
		},
		Clients: []NodeClient{{
			OriginID: 7, Enabled: true,
			Accounts: []NodeAccount{{
				OriginID: 11, PrivateKey: "peer-private",
				PublicKey: "peer-public", PresharedKey: "peer-psk",
				Username: "u", Secret: "s",
			}},
		}},
	})
	got := string(body)

	for _, needed := range []string{
		"server-private", "server-public",
		"peer-private", "peer-public", "peer-psk", "\"u\"", "\"s\"",
	} {
		if !strings.Contains(got, needed) {
			t.Errorf("the node payload is missing %q, which it cannot serve without:\n%s", needed, got)
		}
	}
}

// Empty credentials are omitted rather than sent as blanks, so an OpenVPN
// tunnel's payload does not carry six empty WireGuard fields per peer and the
// other way round.
func TestNodeAccountOmitsCredentialsItDoesNotHave(t *testing.T) {
	body, _ := json.Marshal(NodeAccount{
		OriginID: 11, DeviceName: "Phone", IP: "10.0.0.2", Enabled: true,
		Username: "s7-phone", Secret: "hunter2",
	})
	got := string(body)

	for _, absent := range []string{"privateKey", "publicKey", "presharedKey"} {
		if strings.Contains(got, absent) {
			t.Errorf("an OpenVPN peer carries an empty %q:\n%s", absent, got)
		}
	}
	if !strings.Contains(got, "s7-phone") || !strings.Contains(got, "hunter2") {
		t.Errorf("the credentials that do exist were dropped:\n%s", got)
	}
}
