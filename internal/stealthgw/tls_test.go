package stealthgw

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The anchor gateway, tested as drift is -- real WireGuard through the
// reference client -- and, since it is the server itself, against a decoy
// that records every byte it is given, so a stranger is seen to reach it
// exactly as it came.

const learnHost = "learn.example"

// rawDecoy is the operator's site as the gateway reaches it: it takes a
// request up to its empty line, or up to the peer's close, records the bytes
// as they came, and answers.
type rawDecoy struct {
	ln   net.Listener
	seen chan []byte
	// banner, when set, is said as soon as a connection opens.
	banner string
	// reset has the decoy reset each connection after reading it.
	reset bool
}

const decoyAnswer = "HTTP/1.1 418 I'm a teapot\r\nServer: decoy-httpd/1.0\r\nContent-Length: 23\r\nConnection: close\r\n\r\nthe operator's own site"

func newRawDecoy(t *testing.T, edit func(*rawDecoy)) *rawDecoy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &rawDecoy{ln: ln, seen: make(chan []byte, 64)}
	if edit != nil {
		edit(d)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(c)
		}
	}()
	return d
}

func (d *rawDecoy) serve(c net.Conn) {
	defer c.Close()
	if d.banner != "" {
		io.WriteString(c, d.banner)
	}
	var got []byte
	buf := make([]byte, 4096)
	// A PROXY header, which itself opens with an empty line, is not the
	// request's end.
	for !bytes.Contains(got[proxyHeaderLen(got):], []byte("\r\n\r\n")) {
		// As long as a web server waits for a head: longer than the gateway
		// waits for a line.
		c.SetReadDeadline(time.Now().Add(lineFor + 20*time.Second))
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	// The gateway's own requests are not a visitor's, and nor is the
	// connection it opens beside every line and closes unused for a device.
	if len(got) > 0 && !bytes.Contains(got, []byte("Host: "+learnHost)) {
		d.seen <- got
	}
	if d.reset {
		c.(*net.TCPConn).SetLinger(0)
		return
	}
	io.WriteString(c, decoyAnswer)
}

// proxyHeaderLen is the length of the PROXY header b opens with, 0 when it
// opens with none or with only part of one.
func proxyHeaderLen(b []byte) int {
	if len(b) < 16 || !bytes.HasPrefix(b, []byte("\r\n\r\n\x00\r\nQUIT\n")) {
		return 0
	}
	if n := 16 + int(b[14])<<8 + int(b[15]); n <= len(b) {
		return n
	}
	return 0
}

func (d *rawDecoy) origin() string { return "http://" + d.ln.Addr().String() }

type tlsFixture struct {
	server *wgEnd
	gw     *TLSGateway
	key    stealth.TransportKey
	pin    stealth.Pin
	decoy  *rawDecoy
	ctx    context.Context
}

func newTLSFixture(t *testing.T, edit func(*TLSConfig), decoy *rawDecoy, peers ...wgtypes.Key) *tlsFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port := freePort(t)
	lines := []string{fmt.Sprintf("listen_port=%d", port)}
	for i, p := range peers {
		lines = append(lines, peer(p.PublicKey(), netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})))
	}
	srv := wireguard(t, key(t), lines...)
	if decoy == nil {
		decoy = newRawDecoy(t, nil)
	}
	replayFile := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(replayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	tk, err := stealth.NewTransportKey()
	if err != nil {
		t.Fatal(err)
	}
	cert, pin := testCertificate(t)
	cfg := TLSConfig{
		Listen: "127.0.0.1:0", Path: testPath, Origin: decoy.origin(), Host: learnHost, Certificate: cert,
		WireGuard: fmt.Sprintf("127.0.0.1:%d", port), PrivateKey: srv.key,
		TransportKeys: []stealth.TransportKey{tk}, ReplayFile: replayFile,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if edit != nil {
		edit(&cfg)
	}
	gw, err := NewTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Listen(); err != nil {
		t.Fatal(err)
	}
	go gw.Serve(ctx)
	return &tlsFixture{server: srv, gw: gw, key: tk, pin: pin, decoy: decoy, ctx: ctx}
}

func testCertificate(t *testing.T) (*tls.Certificate, stealth.Pin) {
	t.Helper()
	k, err := stealth.NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := stealth.MakeCertificate(k, "", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	served, err := stealth.TLSCertificate(c, k)
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := stealth.CertificateKeyPin(k)
	return served, pin
}

func (f *tlsFixture) anchor() stealth.Anchor { return stealth.Anchor(f.server.key.PublicKey()) }

func (f *tlsFixture) client(t *testing.T, secret stealth.Secret, priv wgtypes.Key) (*wgEnd, *StreamClient) {
	t.Helper()
	c, err := NewStreamClient(StreamClientConfig{
		Transport: stealth.Anchored, URL: "wss://" + f.gw.Addr().String() + testPath,
		Pins: []stealth.Pin{f.pin}, SelfSigned: true,
		Listen: "127.0.0.1:0", Secret: secret, Anchor: f.anchor(), TransportKey: f.key.Public(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String())), c
}

func (f *tlsFixture) token(t *testing.T, s stealth.Secret) string {
	t.Helper()
	pc, err := stealth.NewProofClient(s, f.anchor(), stealth.Anchored, f.key.Public())
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := pc.Token(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// dial is a TLS connection to the gateway as any visitor makes one.
func (f *tlsFixture) dial(t *testing.T, maxVersion uint16) *tls.Conn {
	t.Helper()
	c, err := tls.Dial("tcp", f.gw.Addr().String(), &tls.Config{InsecureSkipVerify: true, MaxVersion: maxVersion})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	return c
}

func upgradeRequest(target string) string {
	return "GET " + target + " HTTP/1.1\r\nHost: edge.example\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
}

func TestATunnelRunsThroughTLSBothWays(t *testing.T) {
	ck := key(t)
	f := newTLSFixture(t, nil, nil, ck)
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
	if st.Opened != 1 || st.Established != 1 || st.Sessions != 1 || st.Sent < 2 || st.Tunnel != 0 || st.Strangers != 0 {
		t.Fatalf("figures after a tunnel came up: %+v", st)
	}
	select {
	case b := <-f.decoy.seen:
		t.Fatalf("a device's connection reached the decoy: %q", b)
	default:
	}
}

func TestEveryStrangerReachesTheDecoyByteForByte(t *testing.T) {
	ck := key(t)
	f := newTLSFixture(t, nil, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	held := f.token(t, s) // a device's, sent on the wrong path: it must stay good
	for _, req := range []string{
		"GET / HTTP/1.1\r\nHost: edge.example\r\n\r\n",
		"GET /index.html?q=1 HTTP/1.1\r\nHost: edge.example\r\nCookie: a=b\r\n\r\n",
		"POST " + testPath + " HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n",
		"get " + testPath + " HTTP/1.1\r\n\r\n",
		"GET " + testPath + " HTTP/1.0\r\n\r\n",
		"GET /a\"b HTTP/1.1\r\n\r\n",
		"\x00\x01junk\r\n\r\n",
		upgradeRequest(testPath),
		upgradeRequest(testPath + "?not-a-token"),
		upgradeRequest(testPath + "?" + f.token(t, secret(t))), // a device nobody knows
		upgradeRequest(testPath + "x?" + held),                 // the wrong path
	} {
		c := f.dial(t, 0)
		io.WriteString(c, req)
		answer, _ := io.ReadAll(c)
		if string(answer) != decoyAnswer {
			t.Fatalf("%q was answered %q, not by the decoy", req, answer)
		}
		select {
		case got := <-f.decoy.seen:
			if string(got) != req {
				t.Fatalf("the decoy was given %q for %q", got, req)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the decoy never saw %q", req)
		}
	}
	st := f.gw.Stats()
	if st.Opened != 0 || st.Sessions != 0 || st.Strangers != 11 || st.Relayed != 11 || st.Syntax != 5 || st.Proof != 6 {
		t.Fatalf("figures after strangers: %+v", st)
	}
	// The token sent on the wrong path was not taken by it.
	c := f.dial(t, 0)
	io.WriteString(c, upgradeRequest(testPath+"?"+held))
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodGet})
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("a token once sent on the wrong path: %v %v", resp, err)
	}
}

func TestAProofUnderTLS12IsAStrangers(t *testing.T) {
	ck := key(t)
	f := newTLSFixture(t, nil, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	tok := f.token(t, s)
	c := f.dial(t, tls.VersionTLS12)
	if c.ConnectionState().Version != tls.VersionTLS12 {
		t.Fatal("not TLS 1.2")
	}
	io.WriteString(c, upgradeRequest(testPath+"?"+tok))
	if answer, _ := io.ReadAll(c); string(answer) != decoyAnswer {
		t.Fatalf("a proof under TLS 1.2 was answered %q", answer)
	}
	<-f.decoy.seen
	// Nor was it taken.
	c = f.dial(t, 0)
	io.WriteString(c, upgradeRequest(testPath+"?"+tok))
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodGet})
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("the same proof under TLS 1.3: %v %v", resp, err)
	}
}

func TestAProofOpensOnceOverTLSAndAnswersAsTheDecoy(t *testing.T) {
	ck := key(t)
	f := newTLSFixture(t, nil, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	waitFor(t, func() bool { return f.gw.Stats().Learned > 0 })
	tok := f.token(t, s)
	c := f.dial(t, 0)
	io.WriteString(c, upgradeRequest(testPath+"?"+tok))
	br := bufio.NewReader(c)
	var head []string
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if l == "\r\n" {
			break
		}
		head = append(head, strings.TrimRight(l, "\r\n"))
	}
	if len(head) < 7 || head[0] != "HTTP/1.1 101 Switching Protocols" || head[1] != "Server: decoy-httpd/1.0" || !strings.HasPrefix(head[2], "Date: ") ||
		!strings.Contains(strings.Join(head, "\n"), "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") || !strings.HasPrefix(head[len(head)-1], "Set-Cookie: ") {
		t.Fatalf("the upgrade's head: %q", head)
	}
	again := f.dial(t, 0)
	io.WriteString(again, upgradeRequest(testPath+"?"+tok))
	if answer, _ := io.ReadAll(again); string(answer) != decoyAnswer {
		t.Fatalf("a proof seen before was answered %q", answer)
	}
	if st := f.gw.Stats(); st.Opened != 1 || st.Seen != 1 {
		t.Fatalf("one proof sent twice: %+v", st)
	}
}

func TestADevicesHeadThatIsNoUpgradeIsClosedWithNoAnswer(t *testing.T) {
	ck := key(t)
	f := newTLSFixture(t, nil, nil, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	c := f.dial(t, 0)
	io.WriteString(c, "GET "+testPath+"?"+f.token(t, s)+" HTTP/1.1\r\nHost: x\r\n\r\n")
	if answer, err := io.ReadAll(c); len(answer) != 0 || err != nil {
		t.Fatalf("a proof without an upgrade was answered %q (%v)", answer, err)
	}
	waitFor(t, func() bool { return f.gw.Stats().HeadRefused == 1 })
	if st := f.gw.Stats(); st.Sessions != 0 || st.Pool != 0 {
		t.Fatalf("after a head refused: %+v", st)
	}
}

func TestTheDecoySpeakingFirstDecidesTheLine(t *testing.T) {
	d := newRawDecoy(t, func(d *rawDecoy) { d.banner = "220 hello\r\n" })
	f := newTLSFixture(t, nil, d)
	c := f.dial(t, 0)
	io.WriteString(c, "GET /unfinish")
	br := bufio.NewReader(c)
	if l, err := br.ReadString('\n'); err != nil || l != "220 hello\r\n" {
		t.Fatalf("a decoy that spoke first: %q %v", l, err)
	}
	// The rest of the request goes on to it as the visitor sends it.
	io.WriteString(c, "ed HTTP/1.1\r\n\r\n")
	rest, _ := io.ReadAll(br)
	if string(rest) != decoyAnswer {
		t.Fatalf("then: %q", rest)
	}
	if got := <-d.seen; string(got) != "GET /unfinished HTTP/1.1\r\n\r\n" {
		t.Fatalf("the decoy was given %q", got)
	}
	if st := f.gw.Stats(); st.Origin != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestAHalfCloseReachesTheDecoyAndItsAnswerComesBack(t *testing.T) {
	f := newTLSFixture(t, nil, nil)
	c := f.dial(t, 0)
	io.WriteString(c, "GET /partial")
	time.Sleep(100 * time.Millisecond)
	c.CloseWrite()
	answer, _ := io.ReadAll(c)
	if string(answer) != decoyAnswer {
		t.Fatalf("after a half-close: %q", answer)
	}
	if got := <-f.decoy.seen; string(got) != "GET /partial" {
		t.Fatalf("the decoy was given %q", got)
	}
}

func TestADecoyResetResetsThePeer(t *testing.T) {
	d := newRawDecoy(t, func(d *rawDecoy) { d.reset = true })
	f := newTLSFixture(t, nil, d)
	c := f.dial(t, 0)
	io.WriteString(c, "GET / HTTP/1.1\r\n\r\n")
	_, err := io.ReadAll(c)
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("a reset at the decoy reached the peer as %v", err)
	}
}

func TestWithTheDecoyDownAStrangerIsClosedWithNothing(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	gone := "http://" + ln.Addr().String()
	ln.Close()
	f := newTLSFixture(t, func(c *TLSConfig) { c.Origin = gone }, nil)
	c := f.dial(t, 0)
	io.WriteString(c, "GET / HTTP/1.1\r\n\r\n")
	if answer, _ := io.ReadAll(c); len(answer) != 0 {
		t.Fatalf("a stranger was answered %q with the decoy down", answer)
	}
	if st := f.gw.Stats(); st.DecoyDown != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestASlowLineGoesToTheDecoy(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the line's deadline")
	}
	f := newTLSFixture(t, nil, nil)
	c := f.dial(t, 0)
	c.SetDeadline(time.Now().Add(lineFor + 5*time.Second))
	io.WriteString(c, "GET /slow")
	// Nothing comes back until the line's time is up and it is handed on;
	// the decoy then waits for the rest, which comes.
	time.Sleep(lineFor + 500*time.Millisecond)
	io.WriteString(c, " HTTP/1.1\r\n\r\n")
	if answer, _ := io.ReadAll(c); string(answer) != decoyAnswer {
		t.Fatalf("a slow line: %q", answer)
	}
	if got := <-f.decoy.seen; string(got) != "GET /slow HTTP/1.1\r\n\r\n" {
		t.Fatalf("the decoy was given %q", got)
	}
	if st := f.gw.Stats(); st.Slow != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestOneSourceCannotHoldMoreThanItsShareUndecided(t *testing.T) {
	f := newTLSFixture(t, nil, nil)
	first, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	waitFor(t, func() bool { return f.gw.Stats().Undecided == 1 })
	var rest []net.Conn
	for range undecidedPerSource {
		c, err := net.Dial("tcp", f.gw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, c)
	}
	defer func() {
		for _, c := range rest {
			c.Close()
		}
	}()
	// The oldest, which never began TLS, is let go at once rather than at
	// its handshake's deadline.
	first.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := first.Read(make([]byte, 1)); err == nil {
		t.Fatal("the oldest connection was not let go")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the oldest connection was kept past its source's share")
	}
	waitFor(t, func() bool { return f.gw.Stats().Displaced == 1 })
	if st := f.gw.Stats(); st.Undecided != undecidedPerSource {
		t.Fatalf("%+v", st)
	}
}

func TestPinsHoldTheServer(t *testing.T) {
	f := newTLSFixture(t, nil, nil)
	addr := f.gw.Addr().String()
	try := func(cfg *tls.Config) error {
		c, err := tls.Dial("tcp", addr, cfg)
		if err == nil {
			c.Close()
		}
		return err
	}
	good, _ := ClientTLS("127.0.0.1", []stealth.Pin{f.pin}, true, nil)
	if err := try(good); err != nil {
		t.Fatalf("the pinned server: %v", err)
	}
	_, other := testCertificate(t)
	bad, _ := ClientTLS("127.0.0.1", []stealth.Pin{other}, true, nil)
	if err := try(bad); err == nil {
		t.Fatal("a server whose key is not pinned was taken")
	}
	issued, _ := ClientTLS("127.0.0.1", []stealth.Pin{f.pin}, false, nil)
	if err := try(issued); err == nil {
		t.Fatal("a certificate nobody issued passed as issued for its pin")
	}
	if _, err := ClientTLS("x", nil, true, nil); err == nil {
		t.Fatal("pins alone with no pin")
	}
	// A new certificate is served from the next connection on.
	next, nextPin := testCertificate(t)
	f.gw.SetCertificate(next)
	if err := try(good); err == nil {
		t.Fatal("the old pin still held after the certificate changed")
	}
	moved, _ := ClientTLS("127.0.0.1", []stealth.Pin{nextPin}, true, nil)
	if err := try(moved); err != nil {
		t.Fatalf("the new certificate: %v", err)
	}
}

func TestAServerShowingThePinnedCertificateWithoutItsKeyIsRefused(t *testing.T) {
	pinned, pin := testCertificate(t)
	other, _ := testCertificate(t)
	// The pinned certificate, with another key to sign the handshake.
	forged := &tls.Certificate{Certificate: pinned.Certificate, PrivateKey: other.PrivateKey}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*forged}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	cfg, _ := ClientTLS("127.0.0.1", []stealth.Pin{pin}, true, nil)
	c, err := tls.Dial("tcp", ln.Addr().String(), cfg)
	if err == nil {
		c.Close()
		t.Fatal("a server that showed the pinned certificate without its key was taken")
	}
	// Nor is TLS 1.2 taken from a pinned server, where the client checks less.
	ln12, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*pinned}, MaxVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer ln12.Close()
	go func() {
		if c, err := ln12.Accept(); err == nil {
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	if c, err := tls.Dial("tcp", ln12.Addr().String(), cfg); err == nil {
		c.Close()
		t.Fatal("a pinned server was taken over TLS 1.2")
	}
}

func TestProxyHeader(t *testing.T) {
	v4 := proxyHeader(netip.MustParseAddrPort("203.0.113.7:51000"), netip.MustParseAddrPort("192.0.2.1:443"))
	want := append([]byte("\r\n\r\n\x00\r\nQUIT\n\x21\x11\x00\x0c"), 203, 0, 113, 7, 192, 0, 2, 1, 0xc7, 0x38, 0x01, 0xbb)
	if !bytes.Equal(v4, want) {
		t.Fatalf("v4: %x", v4)
	}
	mixed := proxyHeader(netip.MustParseAddrPort("[2001:db8::1]:1"), netip.MustParseAddrPort("192.0.2.1:443"))
	if len(mixed) != 16+36 || mixed[13] != 0x21 || !bytes.Equal(mixed[32:48], netip.MustParseAddr("::ffff:192.0.2.1").AsSlice()) {
		t.Fatalf("mixed: %x", mixed)
	}
}

func TestADecoyWithAProxyHeaderIsToldTheVisitor(t *testing.T) {
	d := newRawDecoy(t, nil)
	f := newTLSFixture(t, func(c *TLSConfig) { c.ProxyProtocol = true }, d)
	c := f.dial(t, 0)
	io.WriteString(c, "GET / HTTP/1.1\r\n\r\n")
	io.ReadAll(c)
	got := <-d.seen
	sig := []byte("\r\n\r\n\x00\r\nQUIT\n")
	if !bytes.HasPrefix(got, sig) || got[13] != 0x11 || !bytes.Equal(got[16:20], []byte{127, 0, 0, 1}) ||
		!bytes.HasSuffix(got, []byte("GET / HTTP/1.1\r\n\r\n")) {
		t.Fatalf("the decoy was given %q", got)
	}
}

func TestATLSGatewayRefusesWhatCannotWork(t *testing.T) {
	tk, _ := stealth.NewTransportKey()
	cert, _ := testCertificate(t)
	base := TLSConfig{Path: testPath, Origin: "http://127.0.0.1:8080", Certificate: cert, WireGuard: "127.0.0.1:1", TransportKeys: []stealth.TransportKey{tk}}
	for name, edit := range map[string]func(*TLSConfig){
		"no decoy":           func(c *TLSConfig) { c.Origin = "" },
		"an https decoy":     func(c *TLSConfig) { c.Origin = "https://127.0.0.1" },
		"a decoy with path":  func(c *TLSConfig) { c.Origin = "http://127.0.0.1/x" },
		"no certificate":     func(c *TLSConfig) { c.Certificate = nil },
		"missing cert files": func(c *TLSConfig) { c.CertFile, c.KeyFile = "/nonexistent.crt", "/nonexistent.key" },
		"a short path":       func(c *TLSConfig) { c.Path = "/short" },
		"no key":             func(c *TLSConfig) { c.TransportKeys = nil },
	} {
		c := base
		edit(&c)
		if _, err := NewTLS(c); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}
