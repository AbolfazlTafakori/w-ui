package stealthgw

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The drift gateway, tested against real WireGuard as veil is: the
// client's datagrams through the reference stream client, and every
// stranger's request through to a decoy that must see it as it came.

const testPath = "/media/7f3a9c2e1b"

type streamFixture struct {
	server *wgEnd
	gw     *StreamGateway
	key    stealth.TransportKey
	decoy  *httptest.Server
	seen   chan string // what the decoy was asked, method and target
	ctx    context.Context
}

func newStreamFixture(t *testing.T, edit func(*StreamConfig), peers ...wgtypes.Key) *streamFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port := freePort(t)
	lines := []string{fmt.Sprintf("listen_port=%d", port)}
	for i, p := range peers {
		lines = append(lines, peer(p.PublicKey(), netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})))
	}
	srv := wireguard(t, key(t), lines...)
	seen := make(chan string, 64)
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.RequestURI()
		w.Header().Set("X-Decoy", "yes")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "the operator's own site")
	}))
	t.Cleanup(decoy.Close)
	replayFile := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(replayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	tk, err := stealth.NewTransportKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := StreamConfig{
		Listen: "127.0.0.1:0", Path: testPath, Origin: decoy.URL,
		WireGuard: fmt.Sprintf("127.0.0.1:%d", port), PrivateKey: srv.key,
		TransportKeys: []stealth.TransportKey{tk}, ReplayFile: replayFile,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if edit != nil {
		edit(&cfg)
	}
	gw, err := NewStream(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Listen(); err != nil {
		t.Fatal(err)
	}
	go gw.Serve(ctx)
	return &streamFixture{server: srv, gw: gw, key: tk, decoy: decoy, seen: seen, ctx: ctx}
}

func (f *streamFixture) anchor() stealth.Anchor { return stealth.Anchor(f.server.key.PublicKey()) }

func (f *streamFixture) url() string { return "ws://" + f.gw.Addr().String() + testPath }

func (f *streamFixture) client(t *testing.T, secret stealth.Secret, priv wgtypes.Key) (*wgEnd, *StreamClient) {
	t.Helper()
	c, err := NewStreamClient(StreamClientConfig{
		URL: f.url(), Listen: "127.0.0.1:0", Secret: secret, Anchor: f.anchor(), TransportKey: f.key.Public(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String())), c
}

func (f *streamFixture) token(t *testing.T, s stealth.Secret) string {
	t.Helper()
	pc, err := stealth.NewProofClient(s, f.anchor(), stealth.Drift, f.key.Public())
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := pc.Token(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// ask sends one request by hand and reads its answer's head.
func (f *streamFixture) ask(t *testing.T, target string, upgrade bool, extra ...string) (*http.Response, net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := "GET " + target + " HTTP/1.1\r\nHost: front.example\r\n"
	if upgrade {
		req += "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	}
	for _, h := range extra {
		req += h + "\r\n"
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, req+"\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	return resp, conn, br
}

func isDecoy(resp *http.Response) bool {
	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("X-Decoy") != "yes" {
		return false
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b) == "the operator's own site"
}

func TestATunnelRunsThroughWSBothWays(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, _ := f.client(t, s, ck)

	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatalf("nothing reached the server: %+v", f.gw.Stats())
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 5*time.Second) {
		t.Fatalf("nothing reached the client: %+v", f.gw.Stats())
	}
	st := f.gw.Stats()
	if st.Opened != 1 || st.Established != 1 || st.Sessions != 1 || st.Sent < 2 || st.Tunnel != 0 || st.Unopened != 0 {
		t.Fatalf("figures after a tunnel came up: %+v", st)
	}
}

func TestEveryStrangerReachesTheDecoyAsItCame(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret(t)}})
	stranger := f.token(t, secret(t))
	for _, tc := range []struct {
		target  string
		upgrade bool
	}{
		{"/", false},
		{"/index.html?q=1", false},
		{testPath, false},
		{testPath, true},
		{testPath + "?not-a-token", true},
		{testPath + "?" + stranger, true},               // a device the gateway does not know
		{testPath + "?" + stranger, false},              // and no upgrade
		{testPath + "x?" + f.token(t, secret(t)), true}, // the wrong path
	} {
		resp, _, _ := f.ask(t, tc.target, tc.upgrade)
		if !isDecoy(resp) {
			t.Fatalf("%s (upgrade %v) was answered %s, not by the decoy", tc.target, tc.upgrade, resp.Status)
		}
		if got := <-f.seen; got != "GET "+tc.target {
			t.Fatalf("the decoy was asked %q for %q", got, tc.target)
		}
	}
	st := f.gw.Stats()
	if st.Opened != 0 || st.Sessions != 0 || st.Decoyed != 8 {
		t.Fatalf("figures after strangers: %+v", st)
	}
}

func TestWithoutADecoyAStrangerGetsNotFound(t *testing.T) {
	f := newStreamFixture(t, func(c *StreamConfig) { c.Origin = "" })
	resp, _, _ := f.ask(t, testPath, true)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a stranger was answered %s", resp.Status)
	}
}

func TestAProofFromAnUntrustedPeerIsAStrangers(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, func(c *StreamConfig) { c.Proxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} }, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	resp, _, _ := f.ask(t, testPath+"?"+f.token(t, s), true)
	if !isDecoy(resp) {
		t.Fatalf("a proof from a peer that is no front was answered %s", resp.Status)
	}
	if st := f.gw.Stats(); st.Opened != 0 || st.Unopened != 0 {
		t.Fatalf("a proof from an untrusted peer was even looked at: %+v", st)
	}
}

func TestAProofOpensOnce(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	tok := f.token(t, s)
	resp, _, _ := f.ask(t, testPath+"?"+tok, true)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("a good proof was answered %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || !strings.Contains(resp.Header.Get("Set-Cookie"), "=") {
		t.Fatalf("the upgrade's headers: %v", resp.Header)
	}
	again, _, _ := f.ask(t, testPath+"?"+tok, true)
	if !isDecoy(again) {
		t.Fatalf("a proof seen before was answered %s", again.Status)
	}
	if st := f.gw.Stats(); st.Opened != 1 || st.Seen != 1 {
		t.Fatalf("one proof sent twice: %+v", st)
	}
}

func TestASessionOpensOnlyOnAnInitiationOrAKeepalive(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Drift, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	resp, conn, br := f.ask(t, testPath+"?"+tok, true)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("a good proof was answered %s", resp.Status)
	}
	send, _ := stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
	// A transport message under an index nobody gave: user data first.
	data := append([]byte{4, 0, 0, 0, 9, 9, 9, 9}, make([]byte, 100)...)
	sealed, _ := send.Seal(nil, data)
	conn.Write(stealth.AppendFrame(nil, stealth.OpBinary, sealed, true))
	op, payload, err := stealth.ReadFrame(br, make([]byte, stealth.MaxSealed), false)
	if err != nil || op != stealth.OpClose || string(payload) != string(stealth.CloseNormal) {
		t.Fatalf("a session opened with user data was not closed with 1000: op %d %x %v", op, payload, err)
	}
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	if st := f.gw.Stats(); st.Tunnel != 1 || st.Sessions != 0 || st.Established != 0 {
		t.Fatalf("figures after user data first: %+v", st)
	}
}

func TestATextFrameEndsTheSession(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	resp, conn, br := f.ask(t, testPath+"?"+f.token(t, s), true)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatal(resp.Status)
	}
	conn.Write(stealth.AppendFrame(nil, 0x1, []byte("hello"), true))
	op, _, err := stealth.ReadFrame(br, make([]byte, stealth.MaxSealed), false)
	if err != nil || op != stealth.OpClose {
		t.Fatalf("a text frame: op %d, %v", op, err)
	}
}

func TestOneDevicesSecretCannotCarryAnothersTunnelOverWS(t *testing.T) {
	ck, other := key(t), key(t)
	f := newStreamFixture(t, nil, ck, other)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}, {PublicKey: other.PublicKey(), Secret: secret(t)}})
	cl, _ := f.client(t, s, other)
	if through(cl, f.server, tuntest.Ping(serverIP, otherIP), 3*time.Second) {
		t.Fatal("a device's tunnel ran under another device's secret")
	}
	if st := f.gw.Stats(); st.Tunnel == 0 || st.Established != 0 {
		t.Fatalf("figures after a borrowed secret: %+v", st)
	}
}

func TestARevokedDeviceLosesItsStreams(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, _ := f.client(t, s, ck)
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatal("the tunnel did not come up")
	}
	f.gw.SetDevices(nil)
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	if st := f.gw.Stats(); st.Sessions != 0 || st.Devices != 0 {
		t.Fatalf("after the device was taken away: %+v", st)
	}
	if through(f.server, cl, tuntest.Ping(clientIP, serverIP), 2*time.Second) {
		t.Fatal("a revoked device was still reached")
	}
}

func TestAClientComesBackAfterItsSessionEnds(t *testing.T) {
	ck := key(t)
	f := newStreamFixture(t, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, c := f.client(t, s, ck)
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatal("the tunnel did not come up")
	}
	// The device is taken away and given back: its session is gone, data
	// cannot open another, and WireGuard, hearing nothing, starts a new
	// handshake that does.
	f.gw.SetDevices(nil)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 30*time.Second) {
		t.Fatalf("the tunnel did not come back: %+v", f.gw.Stats())
	}
	if c.Sessions() < 2 {
		t.Fatalf("the client opened %d sessions", c.Sessions())
	}
}

func TestTheVisitorIsTheLastAddressThatIsNoFront(t *testing.T) {
	g := &StreamGateway{proxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("198.51.100.0/24")}, header: "X-Forwarded-For"}
	peerAddr := netip.MustParseAddr("127.0.0.1")
	for header, want := range map[string]string{
		"":                          "127.0.0.1",
		"203.0.113.7":               "203.0.113.7",
		"10.1.1.1, 203.0.113.7":     "203.0.113.7",
		"203.0.113.7, 198.51.100.4": "203.0.113.7",
		"203.0.113.7:4433":          "203.0.113.7",
		"[2001:db8::1]":             "2001:db8::1",
		"garbage, 203.0.113.7":      "203.0.113.7",
		"203.0.113.7, garbage":      "127.0.0.1",
		"::ffff:203.0.113.9":        "203.0.113.9",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("X-Forwarded-For", header)
		}
		if got := g.visitor(r, peerAddr).String(); got != want {
			t.Errorf("%q: the visitor is %s, not %s", header, got, want)
		}
	}
}

func TestAStreamGatewayRefusesWhatCannotWork(t *testing.T) {
	tk, _ := stealth.NewTransportKey()
	base := StreamConfig{Path: testPath, WireGuard: "127.0.0.1:1", TransportKeys: []stealth.TransportKey{tk}}
	for name, edit := range map[string]func(*StreamConfig){
		"a short path":     func(c *StreamConfig) { c.Path = "/short" },
		"a path without /": func(c *StreamConfig) { c.Path = strings.Repeat("a", 20) },
		"no key":           func(c *StreamConfig) { c.TransportKeys = nil },
		"a decoy of ftp":   func(c *StreamConfig) { c.Origin = "ftp://x" },
	} {
		c := base
		edit(&c)
		if _, err := NewStream(c); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}
