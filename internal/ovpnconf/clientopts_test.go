package ovpnconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func lines(s string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		out[strings.TrimSpace(l)] = true
	}
	return out
}

// Windows keeps to the tunnel's DNS, a client that does not know the option
// passes over it, a UDP client says when it leaves, the kernel sizes the
// buffers, and the app lists the profile by name.
func TestTheProfileCarriesTheConnectionOptions(t *testing.T) {
	iface := testIface(t)
	got := lines(RenderProfile(iface))
	for _, want := range []string{
		"ignore-unknown-option block-outside-dns", "block-outside-dns",
		"explicit-exit-notify 1", "sndbuf 0", "rcvbuf 0", `setenv FRIENDLY_NAME "vpn0"`,
	} {
		if !got[want] {
			t.Errorf("the profile lacks %q", want)
		}
	}
	// The ignore comes before the option, or a client that does not know
	// it stops at it.
	p := RenderProfile(iface)
	if strings.Index(p, "ignore-unknown-option block-outside-dns") > strings.Index(p, "\nblock-outside-dns") {
		t.Error("block-outside-dns comes before the line that lets other clients pass over it")
	}
	// Neither is pushed: a file handed out before them must not be sent an
	// option it was never told to ignore.
	srv, err := RenderServer(iface, testLayout())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(srv, `push "block-outside-dns"`) || strings.Contains(srv, `push "explicit-exit-notify`) {
		t.Error("the server pushes an option old files do not know")
	}
	if s := lines(srv); !s["sndbuf 0"] || !s[`push "sndbuf 0"`] || !s["explicit-exit-notify 1"] {
		t.Errorf("server:\n%s", srv)
	}
}

// TCP closes its own connection, and refuses explicit-exit-notify.
func TestTCPHasNoExitNotify(t *testing.T) {
	iface := testIface(t)
	p := iface.OpenVPN.V
	p.Transport = "tcp"
	iface.OpenVPN = model.JSON(p)
	srv, err := RenderServer(iface, testLayout())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RenderProfile(iface), "explicit-exit-notify") || strings.Contains(srv, "explicit-exit-notify") {
		t.Fatal("explicit-exit-notify on a TCP tunnel")
	}
}

// The single-user rule is untouched: still no duplicate-cn, so a second
// sign-in still ends the first.
func TestTheOptionsLeaveOneSignInOneSession(t *testing.T) {
	srv, err := RenderServer(testIface(t), testLayout())
	if err != nil {
		t.Fatal(err)
	}
	if lines(srv)["duplicate-cn"] {
		t.Fatal("duplicate-cn")
	}
}

// A name typed on the settings page cannot break out of its line.
func TestTheProfileNameIsKeptToOneLine(t *testing.T) {
	named := testIface(t)
	named.ListedAs = "My \"VPN\"\nremote evil.example 1\\ · آلمان"
	p := RenderProfile(named)
	if strings.Contains(p, "remote evil.example") && !strings.Contains(p, "FRIENDLY_NAME \"My VPNremote evil.example 1 · آلمان\"") {
		t.Fatalf("profile:\n%s", p)
	}
	for _, l := range strings.Split(p, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "remote evil") {
			t.Fatal("the name put a line of its own into the file")
		}
	}
	blank := testIface(t)
	blank.ListedAs, blank.Name = "  ", "  "
	if strings.Contains(RenderProfile(blank), "FRIENDLY_NAME") {
		t.Fatal("an empty name was written")
	}
}

// A UDP server answers from the address it was sent to; TCP has its
// connection, and needs nothing.
func TestAUDPServerAnswersFromTheAddressItWasSentTo(t *testing.T) {
	for _, tr := range []string{"udp", "tcp"} {
		i := testIface(t)
		p := i.OpenVPN.V
		p.Transport = tr
		i.OpenVPN = model.JSON(p)
		conf, err := RenderServer(i, testLayout())
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(conf, "\nmultihome\n"); got != (tr == "udp") {
			t.Fatalf("%s: multihome %v", tr, got)
		}
	}
}
