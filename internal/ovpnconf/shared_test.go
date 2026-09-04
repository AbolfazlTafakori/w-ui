package ovpnconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The claim the whole credential model rests on: an OpenVPN profile carries
// nothing about who is connecting.
//
// If that ever stopped being true — a per-customer certificate, a username
// baked into the file — then handing one file to everybody would hand them each
// other's identity, and revoking somebody would mean reissuing files to
// everyone else. So it is checked rather than assumed.
func TestOneProfileServesEveryCustomerOnATunnel(t *testing.T) {
	iface := ovpnIface()

	ali := RenderClient(&model.Account{
		ID: 1, Username: "ali", Secret: "ali-secret",
		DeviceName: "phone", IP: "10.9.0.2",
		PrivateKey: "ali-private", PublicKey: "ali-public",
	}, iface)

	reza := RenderClient(&model.Account{
		ID: 2, Username: "reza", Secret: "reza-secret",
		DeviceName: "laptop", IP: "10.9.0.3",
		PrivateKey: "reza-private", PublicKey: "reza-public",
	}, iface)

	if ali != reza {
		t.Fatalf("two customers got different files; the shared-file model is broken:\n%s\n---\n%s", ali, reza)
	}
	if got := RenderProfile(iface); got != ali {
		t.Error("the tunnel's own profile differs from what a customer is rendered")
	}

	// Nothing identifying may appear, in either direction.
	for _, leak := range []string{
		"ali", "reza", "ali-secret", "reza-secret",
		"ali-private", "ali-public", "phone", "laptop",
		"10.9.0.2", "10.9.0.3",
	} {
		if contains(ali, leak) {
			t.Errorf("the profile carries %q, which belongs to one customer", leak)
		}
	}

	// And it must still ask for credentials, or the file alone would be access.
	if !contains(ali, "auth-user-pass") {
		t.Error("the profile does not ask for a username and password, so the file alone would connect")
	}
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }
