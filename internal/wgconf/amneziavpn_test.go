package wgconf

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// qUncompress is what the AmneziaVPN app does with a key: base64url
// without padding, four bytes of length, zlib.
func qUncompress(t *testing.T, key string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, "vpn://"))
	if err != nil {
		t.Fatal(err)
	}
	n := binary.BigEndian.Uint32(raw[:4])
	r, err := zlib.NewReader(bytes.NewReader(raw[4:]))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if int(n) != len(out) {
		t.Fatalf("length %d, text %d", n, len(out))
	}
	return string(out)
}

func awgIface() *model.Interface {
	return &model.Interface{Name: "awg0", Protocol: model.ProtocolWireGuard, Mode: model.ModeAmnezia,
		ListenPort: 51820, Subnet: "10.8.0.0/24", EndpointHost: "vpn.example.com", MTU: 1420, DNS: "1.1.1.1",
		PublicKey: "pub=", PrivateKey: "priv=",
		AWG: model.JSON(model.AWGParams{Jc: 4, Jmin: 40, Jmax: 90, S1: 30, S2: 40, S3: 20, S4: 15,
			H1: 1000, H2: 2000, H2Max: 2999, H3: 3000, H4: 4000,
			I1: "<b 0xc30000000108><r 8>", I3: "<r 2><b 0x0100>"})}
}

// The key carries the customer's file exactly, and the app sees it as one.
func TestTheAmneziaVPNKeyIsTheFile(t *testing.T) {
	acc := &model.Account{IP: "10.8.0.2", PrivateKey: "cpriv=", PresharedKey: "psk="}
	conf := RenderClient(acc, awgIface())
	key := AmneziaVPNKey(conf)
	if !strings.HasPrefix(key, "vpn://") || strings.ContainsAny(key[6:], "+/=") {
		t.Fatalf("key %q", key)
	}
	got := qUncompress(t, key)
	if got != conf {
		t.Fatalf("decoded:\n%s\nwant:\n%s", got, conf)
	}
	// What the app looks for to read it as a WireGuard file, and nothing
	// that would make it read it as its own format instead.
	if !strings.Contains(got, "[Interface]") || !strings.Contains(got, "[Peer]") || strings.Contains(got, "containers") {
		t.Fatal("the app would not read this as a WireGuard file")
	}
}

func TestRangesAndSpecialsAreWritten(t *testing.T) {
	iface := awgIface()
	acc := &model.Account{IP: "10.8.0.2", PrivateKey: "cpriv="}
	client := RenderClient(acc, iface)
	for _, want := range []string{"H1 = 1000\n", "H2 = 2000-2999\n", "I1 = <b 0xc30000000108><r 8>\n", "I3 = <r 2><b 0x0100>\n"} {
		if !strings.Contains(client, want) {
			t.Errorf("client file lacks %q", want)
		}
	}
	if strings.Contains(client, "I2 =") {
		t.Error("an empty I2 was written")
	}
	server := RenderServer(iface, nil)
	if !strings.Contains(server, "I1 = ") || !strings.Contains(server, "H2 = 2000-2999") {
		t.Errorf("server file:\n%s", server)
	}
	plain := RenderServerPlain(iface, nil)
	if strings.Contains(plain, "I1") || !strings.Contains(plain, "H2 = 2000-2999") {
		t.Errorf("plain server file:\n%s", plain)
	}
}
