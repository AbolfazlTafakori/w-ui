package stealthgw

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-poll gateway, tested as wui-ws's is: real WireGuard through the
// reference client, every stranger's request through to a decoy that must
// see it as it came, body and all, and by hand what whoever learns a
// session's id can try.

type pollFixture struct {
	*streamFixture
	pgw   *PollGateway
	asked chan string // what the decoy was asked: method, target and body's SHA-256
}

func newPollFixture(t *testing.T, peers ...wgtypes.Key) *pollFixture {
	t.Helper()
	asked := make(chan string, 256)
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(b)
		asked <- r.Method + " " + r.URL.RequestURI() + " " + hex.EncodeToString(sum[:8])
		w.Header().Set("X-Decoy", "yes")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "the operator's own site")
	}))
	t.Cleanup(decoy.Close)
	var cfg StreamConfig
	f := newStreamFixture(t, func(c *StreamConfig) { c.Origin = decoy.URL; cfg = *c }, peers...)
	cfg.TransportKeys = []stealth.TransportKey{f.key}
	cfg.ReplayFile += ".poll"
	if err := writeLimit(cfg.ReplayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	p, err := NewPoll(PollConfig{StreamConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Listen(); err != nil {
		t.Fatal(err)
	}
	go p.Serve(f.ctx)
	f.gw = p.StreamGateway
	return &pollFixture{streamFixture: f, pgw: p, asked: asked}
}

func (f *pollFixture) url() string { return "http://" + f.gw.Addr().String() + testPath }

func (f *pollFixture) client(t *testing.T, secret stealth.Secret, priv wgtypes.Key, edit func(*PollClientConfig)) (*wgEnd, *PollClient) {
	t.Helper()
	cfg := PollClientConfig{URL: f.url(), Listen: "127.0.0.1:0", Secret: secret, Anchor: f.anchor(), TransportKey: f.key.Public()}
	if edit != nil {
		edit(&cfg)
	}
	c, err := NewPollClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String())), c
}

// post sends one request by hand on a new connection and reads its answer.
func (f *pollFixture) post(t *testing.T, method, target string, body []byte, extra ...string) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := method + " " + target + " HTTP/1.1\r\nHost: front.example\r\n"
	if body != nil {
		req += "Content-Length: " + strconv.Itoa(len(body)) + "\r\n"
	}
	for _, h := range extra {
		req += h + "\r\n"
	}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	conn.Write(append([]byte(req+"\r\n"), body...))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// decoyed says the answer was the decoy's, and that the decoy was asked
// for target with body as it was sent.
func (f *pollFixture) decoyed(t *testing.T, resp *http.Response, got []byte, method, target string, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("X-Decoy") != "yes" || string(got) != "the operator's own site" {
		t.Fatalf("%s %s was answered %s, not by the decoy", method, target, resp.Status)
	}
	sum := sha256.Sum256(body)
	want := method + " " + target + " " + hex.EncodeToString(sum[:8])
	select {
	case asked := <-f.asked:
		if asked != want {
			t.Fatalf("the decoy was asked %q, not %q", asked, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the decoy was not asked for %s", target)
	}
}

// initiation is a real handshake initiation of priv to the tunnel, caught
// on its way.
func (f *pollFixture) initiation(t *testing.T, priv wgtypes.Key) []byte {
	t.Helper()
	catch, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer catch.Close()
	dev := wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+catch.LocalAddr().String()))
	dev.tun.Outbound <- tuntest.Ping(serverIP, clientIP)
	buf := make([]byte, 2048)
	catch.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := catch.Read(buf)
	if err != nil || n != 148 || buf[0] != 1 {
		t.Fatalf("no initiation was caught: %d bytes, %v", n, err)
	}
	dev.dev.Close()
	return bytes.Clone(buf[:n])
}

// handSession is a session opened by hand: the device's ends of it.
type handSession struct {
	keys stealth.StreamKeys
	send *stealth.StreamSender
	recv *stealth.PollReceiver
	n    uint32
}

// open opens a session with a real initiation, and checks that the answer
// is the tunnel's response.
func (f *pollFixture) open(t *testing.T, s stealth.Secret, priv wgtypes.Key) *handSession {
	t.Helper()
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Poll, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	h := &handSession{keys: keys}
	h.send, _ = stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
	h.recv, _ = stealth.NewPollReceiver(keys.Down)
	body, _ := h.send.AppendBody(nil, f.initiation(t, priv))
	resp, got := f.post(t, "POST", testPath+"?"+tok, body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("a good opening was answered %s", resp.Status)
	}
	pb, err := h.recv.Open(got)
	if err != nil {
		t.Fatalf("the opening's answer does not open: %v", err)
	}
	pb.Commit()
	if d := pb.Datagrams(); len(d) == 0 || d[0][0] != 2 || len(d[0]) != 92 {
		t.Fatalf("the opening's answer is not the tunnel's response: %v", d)
	}
	return h
}

// next is the device's next request, by hand, holding datagrams.
func (h *handSession) next(datagrams ...[]byte) (string, []byte) {
	h.n++
	body, _ := h.send.AppendBody(nil, datagrams...)
	return testPath + "?" + stealth.PollQuery(h.keys.ID, h.n), body
}

func TestATunnelRunsThroughPollBothWays(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, c := f.client(t, s, ck, nil)
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatalf("nothing reached the server: %+v", f.gw.Stats())
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 5*time.Second) {
		t.Fatalf("nothing reached the client: %+v", f.gw.Stats())
	}
	// A burst each way, more than one body holds.
	for i := range 60 {
		big := tuntest.Ping(serverIP, clientIP)
		big = append(big, bytes.Repeat([]byte{byte(i)}, 1300)...)
		cl.tun.Outbound <- big
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 10*time.Second) {
		t.Fatal("the tunnel stopped after a burst")
	}
	st := f.gw.Stats()
	if st.Opened != 1 || st.Established != 1 || st.Sessions != 1 || st.Polls < 3 || st.Tunnel != 0 || st.Bodies != 0 || st.Unordered != 0 {
		t.Fatalf("figures after a tunnel came up: %+v", st)
	}
	if c.Sessions() != 1 || c.Requests() < 3 {
		t.Fatalf("the client opened %d sessions in %d requests", c.Sessions(), c.Requests())
	}
}

func TestEveryPollStrangerReachesTheDecoyAsItCame(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret(t)}})
	pc, _ := stealth.NewProofClient(secret(t), f.anchor(), stealth.Poll, f.key.Public())
	stranger, _, _ := pc.Token(time.Now())
	ws := f.token(t, secret(t)) // a wui-ws token: no proof here
	body := bytes.Repeat([]byte{7}, 40)
	var id [stealth.PollIDLen]byte
	for _, tc := range []struct {
		method, target string
		body           []byte
		extra          []string
	}{
		{"GET", "/", nil, nil},
		{"POST", "/form", body, nil},
		{"GET", testPath + "?" + stranger, nil, nil},
		{"POST", testPath, body, nil},
		{"POST", testPath + "?" + stranger, body, nil},                          // a device the gateway does not know
		{"POST", testPath + "?" + ws, body, nil},                                // another transport's proof
		{"POST", testPath + "x?" + stranger, body, nil},                         // the wrong path
		{"POST", testPath + "?" + stealth.PollQuery(id, 1), body, nil},          // no such session
		{"POST", testPath + "?" + stranger, body[:17], nil},                     // a body too short
		{"PUT", testPath + "?" + stranger, body, nil},                           // not a POST
		{"POST", testPath + "?" + stranger, make([]byte, 65537), nil},           // a body too long
		{"POST", testPath + "?" + stranger, nil, []string{"Content-Length: 0"}}, // none
	} {
		resp, got := f.post(t, tc.method, tc.target, tc.body, tc.extra...)
		f.decoyed(t, resp, got, tc.method, tc.target, tc.body)
	}
	// A body in chunks is no device's, and reaches the decoy whole.
	resp, got := f.post(t, "POST", testPath+"?"+stranger, nil, "Transfer-Encoding: chunked", "\r\n28\r\n"+strings.Repeat("\x07", 40)+"\r\n0")
	f.decoyed(t, resp, got, "POST", testPath+"?"+stranger, body)
	if st := f.gw.Stats(); st.Opened != 0 || st.Sessions != 0 || st.Decoyed != 13 {
		t.Fatalf("figures after strangers: %+v", st)
	}
}

// Whoever learns a session's id can send anything under it: every request
// goes to the decoy as it came, and the session goes on as it was.
func TestNothingAStrangerSendsUnderAnIDDisturbsTheSession(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	h := f.open(t, s, ck)
	target, good := h.next()
	q := func(n uint32) string { return testPath + "?" + stealth.PollQuery(h.keys.ID, n) }
	flipped := bytes.Clone(good)
	flipped[len(flipped)-1] ^= 1
	other, _ := stealth.NewStreamSender(h.keys.Down, stealth.NewPadder(0, 0)) // the wrong direction's key
	wrongKey, _ := other.AppendBody(nil)
	for _, tc := range []struct {
		target string
		body   []byte
	}{
		{target, flipped},
		{target, good[:len(good)-1]},
		{target, wrongKey},
		{target, bytes.Repeat([]byte{0}, len(good))},
		{q(2), good}, // a number ahead
		{q(0), good}, // the opening's
		{q(1) + "0", good},
	} {
		resp, got := f.post(t, "POST", tc.target, tc.body)
		f.decoyed(t, resp, got, "POST", tc.target, tc.body)
	}
	resp, got := f.post(t, "POST", target, good)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the device's own request after the strangers was answered %s", resp.Status)
	}
	pb, err := h.recv.Open(got)
	if err != nil {
		t.Fatalf("its answer does not open: %v", err)
	}
	pb.Commit()
	// id.0 is no later request's query, so it was judged as a token.
	if st := f.gw.Stats(); st.Sessions != 1 || st.Bodies != 4 || st.Unordered != 2 || st.Unopened != 1 {
		t.Fatalf("figures after strangers under an id: %+v", st)
	}
}

// "A front may send a request twice": the same bytes get the same answer,
// and other bytes under the same number are a stranger's.
func TestARepeatGetsTheAnswerKept(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	h := f.open(t, s, ck)
	target, body := h.next()
	_, first := f.post(t, "POST", target, body)
	resp, again := f.post(t, "POST", target, body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(first, again) {
		t.Fatalf("a repeat was answered %s, the same bytes: %v", resp.Status, bytes.Equal(first, again))
	}
	other, _ := stealth.NewStreamSender(h.keys.Up, stealth.NewPadder(0, 0))
	other.AppendBody(nil) // the opening's number
	changed, _ := other.AppendBody(nil)
	resp, got := f.post(t, "POST", target, changed)
	f.decoyed(t, resp, got, "POST", target, changed)
	if st := f.gw.Stats(); st.Repeats != 1 || st.Polls != 2 {
		t.Fatalf("figures after a repeat: %+v", st)
	}
	pb, err := h.recv.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	pb.Commit()
	if _, err := h.recv.Open(again); err == nil {
		t.Fatal("the device opened the same answer twice")
	}
}

// "One request of a session is in flight at a time."
func TestARequestWhileOneIsHeldIsAStrangers(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	h := f.open(t, s, ck)
	t1, b1 := h.next()
	t2, b2 := h.next()
	done := make(chan *http.Response, 1)
	go func() {
		resp, _ := f.post(t, "POST", t1, b1)
		done <- resp
	}()
	time.Sleep(pollHold / 4)
	resp, got := f.post(t, "POST", t2, b2)
	f.decoyed(t, resp, got, "POST", t2, b2)
	if r := <-done; r.StatusCode != http.StatusOK {
		t.Fatalf("the request in flight was answered %s", r.Status)
	}
	if resp, _ := f.post(t, "POST", t2, b2); resp.StatusCode != http.StatusOK {
		t.Fatalf("the next request after the first's answer was answered %s", resp.Status)
	}
}

// A body whose every chunk opens and one does not parse is the device's
// own: the session is over.
func TestABadChunkFromTheDeviceEndsThePollSession(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	h := f.open(t, s, ck)
	sealer, _ := stealth.NewSealer(h.keys.Up)
	sealer.Seal(nil, nil) // the opening's chunk
	sealed, _ := sealer.Seal(nil, []byte{0, 9, 1})
	bad := append([]byte{0, byte(len(sealed))}, sealed...)
	target := testPath + "?" + stealth.PollQuery(h.keys.ID, 1)
	resp, got := f.post(t, "POST", target, bad)
	f.decoyed(t, resp, got, "POST", target, bad)
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	_, good := h.next()
	resp, got = f.post(t, "POST", target, good)
	f.decoyed(t, resp, got, "POST", target, good)
}

func TestALateBodyGoesToTheDecoyWhole(t *testing.T) {
	was := pollBodyFor
	pollBodyFor = 300 * time.Millisecond
	t.Cleanup(func() { pollBodyFor = was })
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	h := f.open(t, s, ck)
	target, body := h.next()
	conn, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: front.example\r\nContent-Length: %d\r\n\r\n", target, len(body))
	conn.Write(body[:10])
	time.Sleep(2 * pollBodyFor)
	conn.Write(body[10:])
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	f.decoyed(t, resp, got, "POST", target, body)
	// The session is as it was: the same request on time is taken.
	if resp, _ := f.post(t, "POST", target, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("the request on time was answered %s", resp.Status)
	}
}

func TestAPollProofOpensOnceAndAKeepaliveAloneOpensNothing(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Poll, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	send, _ := stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
	body, _ := send.AppendBody(nil, wgKeepalive(1))
	target := testPath + "?" + tok
	first := make(chan *http.Response, 1)
	go func() {
		resp, _ := f.post(t, "POST", target, body)
		first <- resp
	}()
	waitFor(t, func() bool { return f.gw.Stats().Opened == 1 })
	resp, got := f.post(t, "POST", target, body)
	f.decoyed(t, resp, got, "POST", target, body)
	// The tunnel says nothing to a keepalive of no session, so the session
	// is never established, and the opening ends at the decoy too.
	select {
	case r := <-first:
		f.decoyed(t, r, []byte("the operator's own site"), "POST", target, body)
	case <-time.After(pendingFor + 5*time.Second):
		t.Fatal("an opening that never was established was held past its deadline")
	}
	if st := f.gw.Stats(); st.Opened != 1 || st.Seen != 1 || st.Established != 0 || st.Sessions != 0 {
		t.Fatalf("figures: %+v", st)
	}
}

func TestAPollSessionOpensOnlyOnAnInitiationOrAKeepalive(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Poll, f.key.Public())
	for _, first := range [][]byte{nil, append([]byte{4, 0, 0, 0, 9, 9, 9, 9}, make([]byte, 100)...)} {
		tok, keys, _ := pc.Token(time.Now())
		send, _ := stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
		var body []byte
		if first == nil {
			body, _ = send.AppendBody(nil)
		} else {
			body, _ = send.AppendBody(nil, first)
		}
		resp, got := f.post(t, "POST", testPath+"?"+tok, body)
		f.decoyed(t, resp, got, "POST", testPath+"?"+tok, body)
	}
	if st := f.gw.Stats(); st.Tunnel != 2 || st.Sessions != 0 || st.Established != 0 {
		t.Fatalf("figures after user data first: %+v", st)
	}
}

func TestADeviceHoldsTwoPollSessions(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	f.open(t, s, ck)
	// WireGuard takes no two initiations of a peer within 20 ms of each
	// other, and the test's come faster than a device's would.
	time.Sleep(100 * time.Millisecond)
	f.open(t, s, ck)
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Poll, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	send, _ := stealth.NewStreamSender(keys.Up, stealth.NewPadder(0, 0))
	body, _ := send.AppendBody(nil, f.initiation(t, ck))
	resp, got := f.post(t, "POST", testPath+"?"+tok, body)
	f.decoyed(t, resp, got, "POST", testPath+"?"+tok, body)
	if st := f.gw.Stats(); st.Sessions != 2 || st.Pool != 1 {
		t.Fatalf("figures after a third session: %+v", st)
	}
}

// cutter is a front that drops a connection after it handed the gateway a
// request and before the answer came back, when told to.
type cutter struct {
	ln   net.Listener
	to   string
	cuts atomic.Int32
	wg   sync.WaitGroup
}

func newCutter(t *testing.T, to string) *cutter {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &cutter{ln: ln, to: to}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			go c.serve(in)
		}
	}()
	return c
}

func (c *cutter) serve(in net.Conn) {
	defer in.Close()
	out, err := net.Dial("tcp", c.to)
	if err != nil {
		return
	}
	defer out.Close()
	go io.Copy(out, in)
	buf := make([]byte, 32<<10)
	for {
		n, err := out.Read(buf)
		if n > 0 {
			if c.cuts.Load() > 0 {
				c.cuts.Add(-1)
				return // the answer never reaches the client
			}
			if _, err := in.Write(buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// A connection the front drops under a request costs nothing: the client
// sends the same bytes again, and the gateway answers from the answer it
// kept.
func TestADroppedAnswerIsAskedForAgainAndTheTunnelGoesOn(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	front := newCutter(t, f.gw.Addr().String())
	cl, c := f.client(t, s, ck, func(cfg *PollClientConfig) { cfg.Dial = front.ln.Addr().String() })
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatalf("the tunnel did not come up: %+v", f.gw.Stats())
	}
	front.cuts.Store(2)
	waitFor(t, func() bool { return front.cuts.Load() == 0 })
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 10*time.Second) {
		t.Fatalf("the tunnel did not go on: %+v", f.gw.Stats())
	}
	if st := f.gw.Stats(); st.Repeats < 2 || st.Sessions != 1 || c.Sessions() != 1 {
		t.Fatalf("figures after two dropped answers: %+v, %d sessions", st, c.Sessions())
	}
}

func TestARevokedDeviceLosesItsPollSessions(t *testing.T) {
	ck := key(t)
	f := newPollFixture(t, ck)
	s := secret(t)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, c := f.client(t, s, ck, nil)
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 10*time.Second) {
		t.Fatal("the tunnel did not come up")
	}
	f.gw.SetDevices(nil)
	waitFor(t, func() bool { return f.gw.Stats().Sessions == 0 })
	if through(f.server, cl, tuntest.Ping(clientIP, serverIP), 2*time.Second) {
		t.Fatal("a revoked device was still reached")
	}
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 30*time.Second) {
		t.Fatalf("the tunnel did not come back: %+v", f.gw.Stats())
	}
	if c.Sessions() < 2 {
		t.Fatalf("the client opened %d sessions", c.Sessions())
	}
}

func TestThePollWaits(t *testing.T) {
	var got []time.Duration
	w := pollFirst
	for range 12 {
		w = slower(w, true)
		got = append(got, w.Round(time.Millisecond))
	}
	want := []time.Duration{150, 225, 337, 506, 759, 1139, 1708, 2562, 3844, 5000, 5000, 5000}
	for i := range want {
		if d := got[i] - want[i]*time.Millisecond; d < -time.Millisecond || d > time.Millisecond {
			t.Fatalf("waits %v", got)
		}
	}
	if slower(pollMost, false) != pollFirst {
		t.Fatal("a wait after something was carried is not the first")
	}
	for range 500 {
		short, long := spread(pollFirst), spread(pollMost)
		if short < 75*time.Millisecond || short > 125*time.Millisecond || long < 3750*time.Millisecond || long > 6250*time.Millisecond {
			t.Fatalf("spread %v %v", short, long)
		}
	}
	if spread(10*time.Minute) != pollLeast {
		t.Fatal("a wait past a minute")
	}
}

func TestAPollClientsRequestHasOneLengthAndABrowsersFields(t *testing.T) {
	c := &PollClient{}
	c.url, _ = url.Parse("https://front.example/media/7f3a9c2e1b")
	req := string(c.post("tok", bytes.Repeat([]byte{7}, 40)))
	head := req[:len(req)-40]
	if strings.Count(strings.ToLower(head), "content-length") != 1 || !strings.HasPrefix(head, "POST /media/7f3a9c2e1b?tok HTTP/1.1\r\nHost: front.example\r\nConnection: keep-alive\r\nContent-Length: 40\r\n") {
		t.Fatalf("the request:\n%s", head)
	}
	for _, f := range []string{"Origin: https://front.example\r\n", "Referer: https://front.example/\r\n", "Sec-Fetch-Mode: cors\r\n", "Content-Type: application/octet-stream\r\n"} {
		if !strings.Contains(head, f) {
			t.Fatalf("no %q in:\n%s", f, head)
		}
	}
}

func TestAPollGatewayRefusesWhatCannotWork(t *testing.T) {
	tk, _ := stealth.NewTransportKey()
	base := StreamConfig{Path: testPath, WireGuard: "127.0.0.1:1", TransportKeys: []stealth.TransportKey{tk}}
	for name, edit := range map[string]func(*StreamConfig){
		"a short path":   func(c *StreamConfig) { c.Path = "/short" },
		"no key":         func(c *StreamConfig) { c.TransportKeys = nil },
		"a decoy of ftp": func(c *StreamConfig) { c.Origin = "ftp://x" },
	} {
		c := base
		edit(&c)
		if _, err := NewPoll(PollConfig{StreamConfig: c}); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if _, err := NewPollClient(PollClientConfig{URL: "ws://x/y"}); err == nil {
		t.Error("a ws:// address was taken for wui-poll")
	}
}
