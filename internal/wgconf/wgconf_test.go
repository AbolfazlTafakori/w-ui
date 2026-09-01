package wgconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func testIface(mode model.InterfaceMode) *model.Interface {
	return &model.Interface{
		Name: "wg0", Subnet: "10.66.0.0/16", ListenPort: 51820,
		EndpointHost: "vpn.example.com", MTU: 1420, DNS: "1.1.1.1",
		PublicKey: "SERVERPUB", PrivateKey: "SERVERPRIV", Mode: mode,
		AWG: model.JSON(model.AWGParams{
			Jc: 4, Jmin: 50, Jmax: 120, S1: 100, S2: 90, S3: 20, S4: 15,
			H1: 111, H2: 222, H3: 333, H4: 444,
		}),
	}
}

func testAccount() *model.Account {
	return &model.Account{
		DeviceName: "iPhone", IP: "10.66.0.2",
		PrivateKey: "CLIENTPRIV", PublicKey: "CLIENTPUB", PresharedKey: "PSK",
	}
}

func TestGatewayIsTheFirstUsableAddress(t *testing.T) {
	addr, bits, err := Gateway("10.66.0.0/16")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	if addr.String() != "10.66.0.1" || bits != 16 {
		t.Errorf("gateway = %s/%d, want 10.66.0.1/16", addr, bits)
	}
}

func TestClientIsConfinedToASlashThirtyTwo(t *testing.T) {
	got := RenderClient(testAccount(), testIface(model.ModeStandard))
	// A wider mask would make the device claim the whole tunnel subnet and
	// blackhole every other customer on that machine.
	if !strings.Contains(got, "Address = 10.66.0.2/32") {
		t.Errorf("client address is not a /32:\n%s", got)
	}
}

func TestClientRoutesEverythingThroughTheTunnel(t *testing.T) {
	got := RenderClient(testAccount(), testIface(model.ModeStandard))
	if !strings.Contains(got, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Error("client is not configured for a full tunnel")
	}
	if !strings.Contains(got, "Endpoint = vpn.example.com:51820") {
		t.Error("client has no endpoint to dial")
	}
	if !strings.Contains(got, "PersistentKeepalive = 25") {
		t.Error("missing keepalive; the NAT mapping would close on idle")
	}
}

func TestObfuscationMatchesOnBothSides(t *testing.T) {
	iface := testIface(model.ModeAmnezia)
	client := RenderClient(testAccount(), iface)
	server, err := RenderServer(iface, nil)
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	// S1-S4 and the H values must be byte-identical at both ends, or every
	// handshake fails with nothing in the logs to explain it.
	for _, line := range []string{
		"Jc = 4", "Jmin = 50", "Jmax = 120",
		"S1 = 100", "S2 = 90", "S3 = 20", "S4 = 15",
		"H1 = 111", "H2 = 222", "H3 = 333", "H4 = 444",
	} {
		if !strings.Contains(client, line) {
			t.Errorf("client missing %q", line)
		}
		if !strings.Contains(server, line) {
			t.Errorf("server missing %q", line)
		}
	}
}

func TestStandardModeEmitsNoObfuscation(t *testing.T) {
	got := RenderClient(testAccount(), testIface(model.ModeStandard))
	for _, line := range []string{"Jc =", "S1 =", "H1 ="} {
		if strings.Contains(got, line) {
			t.Errorf("a standard interface emitted %q; the client would fail to parse it", line)
		}
	}
}

func TestServerConfinesEachPeerToItsOwnAddress(t *testing.T) {
	got, err := RenderServer(testIface(model.ModeStandard), []Peer{
		{PublicKey: "A", AllowedIP: "10.66.0.2/32"},
		{PublicKey: "B", AllowedIP: "10.66.0.3/32", PresharedKey: "PSK"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(got, "Address = 10.66.0.1/16") {
		t.Error("server did not take the gateway address")
	}
	// On the server side AllowedIPs is a filter. A wide mask would let one
	// customer send as another and have the traffic billed to them.
	if !strings.Contains(got, "AllowedIPs = 10.66.0.2/32") ||
		!strings.Contains(got, "AllowedIPs = 10.66.0.3/32") {
		t.Errorf("peers are not confined to their own addresses:\n%s", got)
	}
	if strings.Count(got, "[Peer]") != 2 {
		t.Errorf("expected two peer blocks, got %d", strings.Count(got, "[Peer]"))
	}
}

func TestServerHumanCopyWithholdsThePrivateKey(t *testing.T) {
	got, err := RenderServerHuman(testIface(model.ModeStandard), "eth0")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// This text is shown in a browser and pasted into chat windows.
	if strings.Contains(got, "SERVERPRIV") {
		t.Error("the server private key leaked into the copyable config")
	}
	if !strings.Contains(got, "MASQUERADE") {
		t.Error("missing NAT rules; customers would connect and reach nothing")
	}
}

func TestBadSubnetIsRejected(t *testing.T) {
	iface := testIface(model.ModeStandard)
	iface.Subnet = "not-a-subnet"
	if _, err := RenderServer(iface, nil); err == nil {
		t.Error("an unparseable subnet was accepted")
	}
}
