package stealthgw

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	wgdevice "golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The gateway, tested against real WireGuard: wireguard-go on both ends,
// over channel TUNs, the client's datagrams through the reference client.

var (
	serverIP = netip.MustParseAddr("10.0.0.1")
	clientIP = netip.MustParseAddr("10.0.0.2")
	otherIP  = netip.MustParseAddr("10.0.0.3")
)

type wgEnd struct {
	dev *wgdevice.Device
	tun *tuntest.ChannelTUN
	key wgtypes.Key
}

func key(t *testing.T) wgtypes.Key {
	t.Helper()
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func freePort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// wireguard starts a wireguard-go device with the given configuration lines.
func wireguard(t *testing.T, priv wgtypes.Key, lines ...string) *wgEnd {
	t.Helper()
	ct := tuntest.NewChannelTUN()
	d := wgdevice.NewDevice(ct.TUN(), conn.NewStdNetBind(), wgdevice.NewLogger(wgdevice.LogLevelSilent, ""))
	cfg := "private_key=" + hex.EncodeToString(priv[:]) + "\n" + strings.Join(lines, "\n") + "\n"
	if err := d.IpcSet(cfg); err != nil {
		t.Fatal(err)
	}
	if err := d.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return &wgEnd{dev: d, tun: ct, key: priv}
}

func peer(pub wgtypes.Key, ip netip.Addr, extra ...string) string {
	return strings.Join(append([]string{"public_key=" + hex.EncodeToString(pub[:]), "allowed_ip=" + ip.String() + "/32"}, extra...), "\n")
}

// fixture is a tunnel behind a gateway.
type fixture struct {
	server *wgEnd
	gw     *Gateway
	wgPort int
	ctx    context.Context
}

func newFixture(t *testing.T, peers ...wgtypes.Key) *fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port := freePort(t)
	lines := []string{fmt.Sprintf("listen_port=%d", port)}
	for i, p := range peers {
		lines = append(lines, peer(p.PublicKey(), netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})))
	}
	srv := wireguard(t, key(t), lines...)
	replayFile := filepath.Join(t.TempDir(), "replay")
	// A limit from a run a while ago, so the floor is behind the clock.
	if err := writeLimit(replayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	gw, err := New(Config{
		Listen: "127.0.0.1:0", WireGuard: fmt.Sprintf("127.0.0.1:%d", port),
		PrivateKey: srv.key, MTU: 1420, ReplayFile: replayFile,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Listen(); err != nil {
		t.Fatal(err)
	}
	go gw.Serve(ctx)
	return &fixture{server: srv, gw: gw, wgPort: port, ctx: ctx}
}

func (f *fixture) anchor() stealth.Anchor { return stealth.Anchor(f.server.key.PublicKey()) }

// client starts a device: its reference client with the given transport
// secret, and its WireGuard with the given key, sending to the client.
func (f *fixture) client(t *testing.T, secret stealth.Secret, priv wgtypes.Key) *wgEnd {
	t.Helper()
	c, err := NewClient(ClientConfig{Server: f.gw.Addr().String(), Listen: "127.0.0.1:0", Secret: secret, Anchor: f.anchor(), MTU: 1420})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String()))
}

func secret(t *testing.T) stealth.Secret {
	t.Helper()
	s, err := stealth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// through sends a packet in at one end and says whether it came out at the
// other within the wait.
func through(from, to *wgEnd, packet []byte, wait time.Duration) bool {
	deadline := time.After(wait)
	resend := time.NewTicker(500 * time.Millisecond)
	defer resend.Stop()
	from.tun.Outbound <- packet
	for {
		select {
		case got := <-to.tun.Inbound:
			if bytes.Equal(got, packet) {
				return true
			}
		case <-resend.C:
			select {
			case from.tun.Outbound <- packet:
			default:
			}
		case <-deadline:
			return false
		}
	}
}

func TestATunnelRunsThroughTheGatewayBothWays(t *testing.T) {
	ck := key(t)
	f := newFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl := f.client(t, s, ck)

	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatalf("nothing reached the server: %+v", f.gw.Stats())
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 5*time.Second) {
		t.Fatalf("nothing reached the client: %+v", f.gw.Stats())
	}
	// Sent counts a hair behind the ping it carried, so let it settle rather
	// than reading it the instant the ping arrives -- otherwise a slow runner
	// under -race reads the count one short.
	waitFor(t, func() bool { return f.gw.Stats().Sent >= 2 })
	st := f.gw.Stats()
	if st.Opened < 1 || st.Established < 1 || st.Sent < 2 || st.Tunnel != 0 || st.Unopened != 0 {
		t.Fatalf("figures after a tunnel came up: %+v", st)
	}
}

func TestAStrangerGetsSilence(t *testing.T) {
	ck := key(t)
	f := newFixture(t, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret(t)}})
	c, err := net.DialUDP("udp", nil, f.gw.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Random bytes, zeros, a WireGuard initiation in the clear, and openings
	// of a device the gateway does not know, of the right shape.
	pad := stealth.NewPadder(0, 0)
	stranger, _, _ := stealth.NewUDPClient(secret(t), f.anchor()).Open(nil, time.Now(), append([]byte{4, 0, 0, 0}, make([]byte, 28)...), &pad, 1482)
	for _, p := range [][]byte{bytes.Repeat([]byte{0x5a}, 300), make([]byte, 148), append([]byte{1, 0, 0, 0}, make([]byte, 144)...), stranger, {1, 2, 3}} {
		c.Write(p)
	}
	c.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	if n, err := c.Read(make([]byte, 2048)); err == nil {
		t.Fatalf("the gateway answered a stranger with %d bytes", n)
	}
	st := f.gw.Stats()
	if st.Unknown < 4 || st.Short != 1 || st.Opened != 0 || st.Sent != 0 {
		t.Fatalf("figures after strangers: %+v", st)
	}
}

func TestAnOpeningOpensOnceFromAnywhere(t *testing.T) {
	ck := key(t)
	f := newFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	pad := stealth.NewPadder(0, 0)
	opening, _, err := stealth.NewUDPClient(s, f.anchor()).Open(nil, time.Now(), append([]byte{4, 0, 0, 0}, make([]byte, 28)...), &pad, 1482)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		c, err := net.DialUDP("udp", nil, f.gw.Addr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		c.Write(opening)
		c.Close()
	}
	waitFor(t, func() bool { st := f.gw.Stats(); return st.Opened+st.Seen == 3 })
	if st := f.gw.Stats(); st.Opened != 1 || st.Seen != 2 {
		t.Fatalf("one opening sent from three addresses: %+v", st)
	}
}

func TestOneDevicesSecretCannotCarryAnothersTunnel(t *testing.T) {
	ck, other := key(t), key(t)
	f := newFixture(t, ck, other)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}, {PublicKey: other.PublicKey(), Secret: secret(t)}})
	// The device whose WireGuard key is other, with ck's transport secret.
	cl := f.client(t, s, other)
	if through(cl, f.server, tuntest.Ping(serverIP, otherIP), 3*time.Second) {
		t.Fatal("a device's tunnel ran under another device's secret")
	}
	if st := f.gw.Stats(); st.Tunnel == 0 || st.Established != 0 {
		t.Fatalf("figures after a borrowed secret: %+v", st)
	}
}

func TestARevokedDeviceLosesItsSessions(t *testing.T) {
	ck := key(t)
	f := newFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl := f.client(t, s, ck)
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatal("the tunnel did not come up")
	}
	f.gw.SetDevices(nil)
	if st := f.gw.Stats(); st.Slots != 0 || st.Devices != 0 {
		t.Fatalf("after the device was taken away: %+v", st)
	}
	if through(f.server, cl, tuntest.Ping(clientIP, serverIP), 2*time.Second) {
		t.Fatal("a revoked device was still reached")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
}

// An initiation wireguard-go made is the device's to the responder, and
// nobody else's, and no changed byte of it is.
func TestTheResponderKnowsWhoseAnInitiationIs(t *testing.T) {
	srv, ck, other := key(t), key(t), key(t)
	catch, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer catch.Close()
	cl := wireguard(t, ck, peer(srv.PublicKey(), serverIP, "endpoint="+catch.LocalAddr().String()))
	cl.tun.Outbound <- tuntest.Ping(serverIP, clientIP)
	buf := make([]byte, 2048)
	catch.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := catch.Read(buf)
	if err != nil || !isInitiation(buf[:n]) {
		t.Fatalf("no initiation: %v", err)
	}
	msg := buf[:n]

	r, err := newResponder(srv)
	if err != nil {
		t.Fatal(err)
	}
	ss, ok := r.static(ck.PublicKey())
	if !ok || !r.from(msg, ck.PublicKey(), ss) {
		t.Fatal("the device's own initiation was not taken as its")
	}
	oss, _ := r.static(other.PublicKey())
	if r.from(msg, other.PublicKey(), oss) || r.from(msg, ck.PublicKey(), oss) {
		t.Fatal("an initiation was taken as another device's")
	}
	if wrong, _ := newResponder(other); wrong.from(msg, ck.PublicKey(), ss) {
		t.Fatal("another tunnel took the initiation")
	}
	for i := range 132 { // every byte but MAC2, which a responder under load alone checks
		changed := bytes.Clone(msg)
		changed[i] ^= 0x40
		if r.from(changed, ck.PublicKey(), ss) {
			t.Fatalf("byte %d changed and the initiation still passed", i)
		}
	}
	if _, ok := r.static([32]byte{}); ok {
		t.Fatal("a small-order key gave a secret")
	}
}
