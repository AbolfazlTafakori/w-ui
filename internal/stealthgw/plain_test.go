package stealthgw

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-plain gateway, tested as wui-ws's is, through the same fixture
// with the gateway swapped: real WireGuard through the reference client, and
// every stranger's request through to a decoy that must see it as it came.
// On top, what a reader and writer of the plain path can try.

const testCookie = "sid_3f"

type plainFixture struct {
	*streamFixture
	pgw *PlainGateway
}

func newPlainFixture(t *testing.T, peers ...wgtypes.Key) *plainFixture {
	t.Helper()
	var cfg StreamConfig
	// The wui-ws fixture is made, and its settings taken for a wui-plain
	// gateway on a port of its own; the wui-ws gateway is left unused.
	f := newStreamFixture(t, func(c *StreamConfig) { cfg = *c }, peers...)
	cfg.TransportKeys = []stealth.TransportKey{f.key}
	cfg.ReplayFile += ".plain"
	if err := writeLimit(cfg.ReplayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlain(PlainConfig{StreamConfig: cfg, Cookie: testCookie})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Listen(); err != nil {
		t.Fatal(err)
	}
	go p.Serve(f.ctx)
	f.gw = p.StreamGateway // so the fixture's helpers dial and count this one
	return &plainFixture{streamFixture: f, pgw: p}
}

func (f *plainFixture) client(t *testing.T, secret stealth.Secret, priv wgtypes.Key) (*wgEnd, *StreamClient) {
	t.Helper()
	c, err := NewStreamClient(StreamClientConfig{
		Transport: stealth.Plain, Cookie: testCookie,
		URL: f.url(), Listen: "127.0.0.1:0", Secret: secret, Anchor: f.anchor(), TransportKey: f.key.Public(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String())), c
}

func (f *plainFixture) plainToken(t *testing.T, s stealth.Secret) (string, stealth.StreamKeys) {
	t.Helper()
	pc, err := stealth.NewProofClient(s, f.anchor(), stealth.Plain, f.key.Public())
	if err != nil {
		t.Fatal(err)
	}
	tok, keys, err := pc.Token(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok, keys
}

func cookie(tok string) string { return "Cookie: " + testCookie + "=" + tok }

func TestATunnelRunsThroughPlainBothWays(t *testing.T) {
	ck := key(t)
	f := newPlainFixture(t, ck)
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

// The answer to a proof sets no cookie, and takes no extension.
func TestAPlainUpgradeSetsNoCookie(t *testing.T) {
	ck := key(t)
	f := newPlainFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	tok, _ := f.plainToken(t, s)
	resp, _, _ := f.ask(t, testPath, true, cookie(tok), "Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("a good proof was answered %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || resp.Header.Get("Date") == "" {
		t.Fatalf("the upgrade's headers: %v", resp.Header)
	}
	for _, h := range []string{"Set-Cookie", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol"} {
		if _, ok := resp.Header[h]; ok {
			t.Fatalf("the upgrade has %s: %v", h, resp.Header)
		}
	}
}

func TestEveryPlainStrangerReachesTheDecoyAsItCame(t *testing.T) {
	ck := key(t)
	f := newPlainFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	stranger, _ := f.plainToken(t, secret(t))
	fresh := func() string { tok, _ := f.plainToken(t, s); return tok }
	ws := f.token(t, s) // the device's own wui-ws token
	for _, tc := range []struct {
		what    string
		target  string
		upgrade bool
		extra   []string
	}{
		{"the site", "/", false, nil},
		{"the path alone", testPath, true, nil},
		{"no upgrade", testPath, false, []string{cookie(fresh())}},
		{"the token in the query", testPath + "?" + fresh(), true, nil},
		{"a query beside the cookie", testPath + "?x=1", true, []string{cookie(fresh())}},
		{"an empty query beside the cookie", testPath + "?", true, []string{cookie(fresh())}},
		{"the wrong path", testPath + "x", true, []string{cookie(fresh())}},
		{"another cookie's name", testPath, true, []string{"Cookie: other=" + fresh()}},
		{"two Cookie fields", testPath, true, []string{cookie(fresh()), "Cookie: theme=dark"}},
		{"the name twice", testPath, true, []string{cookie(fresh()) + "; " + testCookie + "=x"}},
		{"a quoted token", testPath, true, []string{"Cookie: " + testCookie + "=\"" + fresh() + "\""}},
		{"a device nobody has", testPath, true, []string{cookie(stranger)}},
		{"a wui-ws token", testPath, true, []string{cookie(ws)}},
		{"text that is no token", testPath, true, []string{cookie("aGVsbG8gd29ybGQ")}},
	} {
		resp, _, _ := f.ask(t, tc.target, tc.upgrade, tc.extra...)
		if !isDecoy(resp) {
			t.Fatalf("%s was answered %s, not by the decoy", tc.what, resp.Status)
		}
		if got := <-f.seen; got != "GET "+tc.target {
			t.Fatalf("%s: the decoy was asked %q for %q", tc.what, got, tc.target)
		}
	}
	if st := f.gw.Stats(); st.Opened != 0 || st.Sessions != 0 || st.Decoyed != 14 {
		t.Fatalf("figures after strangers: %+v", st)
	}
}

// Someone on the path who reads a device's request and sends its token
// first is answered, since the token is good, but cannot say anything on
// the session: its keys need the device's secret. Whatever it sends ends
// the session and reaches nobody's tunnel, and the device's own request
// with that token, coming second, is a stranger's.
func TestATokenReadOffThePathOpensNothingUsable(t *testing.T) {
	ck := key(t)
	f := newPlainFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	tok, _ := f.plainToken(t, s)

	resp, conn, br := f.ask(t, testPath, true, cookie(tok))
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("the token was answered %s", resp.Status)
	}
	// The best the reader can do: a chunk sealed under a key of its own
	// guessing, of a fitting size.
	guess := make([]byte, 32)
	rand.Read(guess)
	send, _ := stealth.NewStreamSender(guess, stealth.NewPadder(0, 0))
	sealed, _ := send.Seal(nil, make([]byte, 148))
	conn.Write(stealth.AppendFrame(nil, stealth.OpBinary, sealed, true))
	op, payload, err := stealth.ReadFrame(br, make([]byte, stealth.MaxSealed), false)
	if err != nil || op != stealth.OpClose || !bytes.Equal(payload, stealth.CloseNormal) {
		t.Fatalf("a chunk under the wrong key: op %d %x %v", op, payload, err)
	}
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	if st := f.gw.Stats(); st.Unopened != 1 || st.Established != 0 {
		t.Fatalf("figures after a chunk under the wrong key: %+v", st)
	}

	again, _, _ := f.ask(t, testPath, true, cookie(tok))
	if !isDecoy(again) {
		t.Fatalf("the token, the second time, was answered %s", again.Status)
	}
}

// A frame written into a running session by someone on the path ends it.
// WireGuard on the device starts a new one, which comes up.
func TestAForgedFrameEndsAPlainSessionAndNothingMore(t *testing.T) {
	ck := key(t)
	f := newPlainFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	tok, keys := f.plainToken(t, s)
	resp, conn, br := f.ask(t, testPath, true, cookie(tok))
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatal(resp.Status)
	}
	// The device's keepalive opens the session; a flipped bit in the next
	// chunk is what a writer on the path can do to it.
	send, _ := stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
	ka := append([]byte{4, 0, 0, 0}, make([]byte, 28)...) // a keepalive: type 4, 32 bytes
	first, _ := send.Seal(nil, ka)
	conn.Write(stealth.AppendFrame(nil, stealth.OpBinary, first, true))
	next, _ := send.Seal(nil, ka)
	next[len(next)/2] ^= 0x01
	conn.Write(stealth.AppendFrame(nil, stealth.OpBinary, next, true))
	for {
		op, _, err := stealth.ReadFrame(br, make([]byte, stealth.MaxSealed), false)
		if err != nil {
			t.Fatalf("the session was not closed: %v", err)
		}
		if op == stealth.OpClose {
			break
		}
	}
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	if st := f.gw.Stats(); st.Unopened != 1 {
		t.Fatalf("figures after a forged frame: %+v", st)
	}
}

func TestAPlainGatewayRefusesWhatCannotWork(t *testing.T) {
	tk, _ := stealth.NewTransportKey()
	base := PlainConfig{
		StreamConfig: StreamConfig{Path: testPath, Origin: "http://127.0.0.1:8080", WireGuard: "127.0.0.1:1", TransportKeys: []stealth.TransportKey{tk}},
		Cookie:       testCookie,
	}
	for name, edit := range map[string]func(*PlainConfig){
		"no decoy":                func(c *PlainConfig) { c.Origin = "" },
		"no cookie":               func(c *PlainConfig) { c.Cookie = "" },
		"a cookie with a blank":   func(c *PlainConfig) { c.Cookie = "s d" },
		"a cookie with =":         func(c *PlainConfig) { c.Cookie = "s=d" },
		"a cookie name too long":  func(c *PlainConfig) { c.Cookie = strings.Repeat("a", stealth.CookieMax+1) },
		"a short path":            func(c *PlainConfig) { c.Path = "/short" },
		"no key":                  func(c *PlainConfig) { c.TransportKeys = nil },
		"a decoy that is no site": func(c *PlainConfig) { c.Origin = "ftp://x" },
	} {
		c := base
		edit(&c)
		if _, err := NewPlain(c); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if _, err := NewPlain(base); err != nil {
		t.Fatalf("a gateway that can work was refused: %v", err)
	}
}

// The plain request a client sends: a browser's, the token in its cookie and
// nowhere else.
func TestAPlainClientsRequestCarriesItsTokenInACookieAlone(t *testing.T) {
	tk, _ := stealth.NewTransportKey()
	c, err := NewStreamClient(StreamClientConfig{
		Transport: stealth.Plain, Cookie: testCookie, URL: "ws://plain.example.com/media/7f3a9c2e1b",
		Listen: "127.0.0.1:0", Secret: secret(t), TransportKey: tk.Public(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.local.Close()
	req := c.request("TOKEN_text-1", "dGhlIHNhbXBsZSBub25jZQ==")
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(req)))
	if err != nil {
		t.Fatal(err)
	}
	if r.RequestURI != "/media/7f3a9c2e1b" || r.Host != "plain.example.com" || r.Header.Get("Origin") != "http://plain.example.com" {
		t.Fatalf("the request: %s %s, Host %s, Origin %s", r.Method, r.RequestURI, r.Host, r.Header.Get("Origin"))
	}
	if tok, ok := stealth.CookieToken(r.Header, testCookie); !ok || tok != "TOKEN_text-1" {
		t.Fatalf("the token in the cookie: %q", tok)
	}
	if _, ok := stealth.WebSocketKey(r); !ok || !strings.Contains(r.UserAgent(), "Chrome/") {
		t.Fatalf("not a browser's upgrade: %v", r.Header)
	}
	for name, cfg := range map[string]StreamClientConfig{
		"wss://":    {Transport: stealth.Plain, Cookie: testCookie, URL: "wss://plain.example.com/media/7f3a9c2e1b"},
		"no cookie": {Transport: stealth.Plain, URL: "ws://plain.example.com/media/7f3a9c2e1b"},
	} {
		cfg.Listen, cfg.TransportKey = "127.0.0.1:0", tk.Public()
		if c, err := NewStreamClient(cfg); err == nil {
			c.local.Close()
			t.Errorf("a plain client with %s was made", name)
		}
	}
}
