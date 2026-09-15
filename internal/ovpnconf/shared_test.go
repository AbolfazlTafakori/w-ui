package ovpnconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The claim the credential model rests on: the tunnel's profile carries
// nothing about who is connecting, and a customer's file is that profile
// plus their own username and password -- never anyone else's, never a key.
//
// If a per-customer certificate ever crept in, revoking somebody would mean
// reissuing files to everyone else. So it is checked rather than assumed.
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

	shared := RenderProfile(iface)
	strip := func(s string) string {
		i := strings.Index(s, "\n<auth-user-pass>")
		if i < 0 {
			return s
		}
		return s[:i]
	}
	if strip(ali) != strip(reza) || strip(ali) != shared {
		t.Fatalf("apart from their own credentials two customers got different files; the shared-file model is broken:\n%s\n---\n%s", ali, reza)
	}

	// The tunnel's profile names nobody.
	for _, leak := range []string{
		"ali", "reza", "ali-secret", "reza-secret",
		"ali-private", "ali-public", "phone", "laptop",
		"10.9.0.2", "10.9.0.3",
	} {
		if contains(shared, leak) {
			t.Errorf("the tunnel's profile carries %q, which belongs to one customer", leak)
		}
	}
	// A customer's file carries their own credentials and nothing else of theirs.
	if !contains(ali, "<auth-user-pass>\nali\nali-secret\n</auth-user-pass>") {
		t.Errorf("ali's file does not carry ali's credentials:\n%s", ali)
	}
	for _, leak := range []string{"reza", "ali-private", "ali-public", "phone", "10.9.0.2"} {
		if contains(ali, leak) {
			t.Errorf("ali's file carries %q", leak)
		}
	}
	// And it still declares the login, or the block would mean nothing.
	if !contains(ali, "auth-user-pass\n") {
		t.Error("the profile does not declare a username-and-password login")
	}
	// A device with no credentials yet gets the bare profile.
	if got := RenderClient(&model.Account{DeviceName: "x"}, iface); got != shared {
		t.Error("a device without credentials should get the tunnel's profile as is")
	}
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }
