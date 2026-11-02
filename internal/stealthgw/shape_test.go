package stealthgw

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The mold gateway, tested as relay's is: a session opened by hand in
// the operator's shape, a later request and its repeat, the stream, and every
// stranger's request through to the decoy as it came.

type shapeFixture struct {
	*streamFixture
	sgw   *ShapeGateway
	shape stealth.Shape
	asked chan string
}

func newShapeFixture(t *testing.T, shape stealth.Shape, peers ...wgtypes.Key) *shapeFixture {
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
	cfg.ReplayFile += ".shape"
	if err := writeLimit(cfg.ReplayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	p, err := NewShape(ShapeConfig{StreamConfig: cfg, Shape: shape})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Listen(); err != nil {
		t.Fatal(err)
	}
	go p.Serve(f.ctx)
	f.gw = p.StreamGateway
	return &shapeFixture{streamFixture: f, sgw: p, shape: shape, asked: asked}
}

// send writes one request by hand and reads its whole answer.
func (f *shapeFixture) send(t *testing.T, parts stealth.ShapeParts, accept bool) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	conn.Write(f.raw(parts, accept))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: f.shape.Method})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// raw is the bytes of a request of the shape.
func (f *shapeFixture) raw(parts stealth.ShapeParts, accept bool) []byte {
	req := f.shape.Method + " " + parts.Target + " HTTP/1.1\r\nHost: front.example\r\n"
	if parts.Header != "" {
		req += f.shape.Name + ": " + parts.Header + "\r\n"
	}
	if accept {
		req += "Accept: text/event-stream\r\n"
	}
	if f.shape.Bodied() {
		req += "Content-Length: " + strconv.Itoa(len(parts.Body)) + "\r\n"
	}
	return append([]byte(req+"\r\n"), parts.Body...)
}

func (f *shapeFixture) decoyed(t *testing.T, resp *http.Response, got []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("X-Decoy") != "yes" || string(got) != "the operator's own site" {
		t.Fatalf("answered %s, not by the decoy", resp.Status)
	}
}

// initiation is a real handshake initiation of priv, caught on its way.
func (f *shapeFixture) initiation(t *testing.T, priv wgtypes.Key) []byte {
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
		t.Fatalf("no initiation caught: %d %v", n, err)
	}
	dev.dev.Close()
	return bytes.Clone(buf[:n])
}

// keepalive is a WireGuard keepalive in shape: type 4, then 28 zero bytes.
func keepalive() []byte { return append([]byte{4, 0, 0, 0}, make([]byte, 28)...) }

// handShape is a session opened by hand.
type handShape struct {
	keys stealth.StreamKeys
	send *stealth.ShapeSender
	recv *stealth.ShapeReceiver
}

// openSession opens a session with a real initiation and checks the opening
// answer carries the tunnel's response.
func (f *shapeFixture) openSession(t *testing.T, s stealth.Secret, priv wgtypes.Key) *handShape {
	t.Helper()
	pc, _ := stealth.NewProofClient(s, f.anchor(), stealth.Mold, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	h := &handShape{keys: keys}
	h.send, _ = stealth.NewShapeSender(keys.Up, stealth.NewPadder(0, 0))
	h.recv, _ = stealth.NewShapeReceiver(keys.Down)
	list, err := h.send.AppendChunk(nil, 0, 0, true, stealth.ShapeDataMax, f.initiation(t, priv))
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := stealth.BuildParts(f.shape, testPath, tok, list)
	if !ok {
		t.Fatal("opening over limit")
	}
	resp, body := f.send(t, parts, false)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("opening answered %s", resp.Status)
	}
	chunks, err := h.recv.OpenList(0, body)
	if err != nil {
		t.Fatalf("opening answer did not open: %v", err)
	}
	var ds [][]byte
	for _, c := range chunks {
		ds = append(ds, c...)
	}
	if len(ds) == 0 || ds[0][0] != 2 {
		t.Fatalf("opening answer did not carry the tunnel's response: %+v", ds)
	}
	return h
}

func TestShapeStrangersGoToTheDecoy(t *testing.T) {
	ck := key(t)
	secret, _ := stealth.NewSecret()
	shape, _ := stealth.Preset("get-poll")
	f := newShapeFixture(t, shape, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret}})

	// A wrong method, a wrong path, a junk query, and an id nobody has.
	var badID [stealth.PollIDLen]byte
	cases := []stealth.ShapeParts{
		{Target: testPath + "?not-a-token"},
		{Target: testPath + "x?" + stealth.ShapeQuery(badID, 1) + ".AAAA"},
		{Target: testPath + "?" + stealth.ShapeQuery(badID, 1) + "." + stealth.ShapeQuery(badID, 1)},
	}
	for _, parts := range cases {
		resp, got := f.send(t, parts, false)
		f.decoyed(t, resp, got)
	}
	// A POST when the shape is GET.
	wrong := *f
	wrong.shape = stealth.Shape{Method: "POST", Place: stealth.InBody, Limit: 65536}
	resp, got := wrong.send(t, stealth.ShapeParts{Target: testPath + "?x", Body: bytes.Repeat([]byte("a"), 40)}, false)
	f.decoyed(t, resp, got)
}

func TestShapePollSession(t *testing.T) {
	ck := key(t)
	secret, _ := stealth.NewSecret()
	shape, _ := stealth.Preset("get-poll")
	f := newShapeFixture(t, shape, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret}})

	h := f.openSession(t, secret, ck)

	// A later request n=1 with a keepalive is accepted and answered 200.
	list, err := h.send.AppendChunk(nil, 1, 0, false, stealth.ShapeDataMax, keepalive())
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := stealth.BuildParts(shape, testPath, stealth.ShapeQuery(h.keys.ID, 1), list)
	if !ok {
		t.Fatal("later over limit")
	}
	resp, body := f.send(t, parts, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("later answered %s", resp.Status)
	}
	if _, err := h.recv.OpenList(1, body); err != nil {
		t.Fatalf("later answer did not open at request 1: %v", err)
	}

	// The same request again: the kept answer, byte for byte.
	resp2, body2 := f.send(t, parts, false)
	if resp2.StatusCode != http.StatusOK || !bytes.Equal(body, body2) {
		t.Fatalf("a repeat was not answered the same")
	}

	// n=1 with other data is the decoy's, and the session is untouched.
	other, _ := h.send.AppendChunk(nil, 1, 0, false, stealth.ShapeDataMax, keepalive(), keepalive())
	bad, _ := stealth.BuildParts(shape, testPath, stealth.ShapeQuery(h.keys.ID, 1), other)
	resp3, got3 := f.send(t, bad, false)
	f.decoyed(t, resp3, got3)
}

func TestShapeStream(t *testing.T) {
	ck := key(t)
	secret, _ := stealth.NewSecret()
	shape, _ := stealth.Preset("post-stream")
	f := newShapeFixture(t, shape, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: secret}})

	pc, _ := stealth.NewProofClient(secret, f.anchor(), stealth.Mold, f.key.Public())
	tok, keys, _ := pc.Token(time.Now())
	send, _ := stealth.NewShapeSender(keys.Up, stealth.NewPadder(0, 0))
	recv, _ := stealth.NewShapeReceiver(keys.Down)
	list, err := send.AppendChunk(nil, 0, 0, true, stealth.ShapeDataMax, f.initiation(t, ck))
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := stealth.BuildParts(shape, testPath, tok, list)

	conn, err := net.Dial("tcp", f.gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	conn.Write(f.raw(parts, true))

	// Read the head, then feed the rest to the stream reader until an event.
	br := bufio.NewReader(conn)
	for {
		linebytes, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("reading head: %v", err)
		}
		if string(linebytes) == "\r\n" {
			break
		}
	}
	reader := stealth.NewSseReader()
	var events [][]byte
	buf := make([]byte, 4096)
	deadline := time.Now().Add(8 * time.Second)
	for len(events) == 0 && time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := br.Read(buf)
		if n > 0 {
			if _, ferr := reader.Feed(buf[:n], &events); ferr != nil {
				t.Fatalf("stream fault: %v", ferr)
			}
		}
		if err != nil {
			break
		}
	}
	if len(events) == 0 {
		t.Fatal("the stream carried no event")
	}
	number, _ := stealth.ShapeNumber(0, 0)
	var ds [][]byte
	if err := recv.OpenEvent(number, events[0], func(d []byte) { ds = append(ds, d) }); err != nil {
		t.Fatalf("the first event did not open: %v", err)
	}
	if len(ds) == 0 || ds[0][0] != 2 {
		t.Fatalf("the first event did not carry the tunnel's response: %+v", ds)
	}
}

// client wires a real WireGuard device to a running ShapeClient.
func (f *shapeFixture) client(t *testing.T, s stealth.Secret, priv wgtypes.Key) (*wgEnd, *ShapeClient) {
	t.Helper()
	c, err := NewShapeClient(ShapeClientConfig{
		URL: "http://" + f.gw.Addr().String() + testPath, Listen: "127.0.0.1:0",
		Secret: s, Anchor: f.anchor(), TransportKey: f.key.Public(), Shape: f.shape,
	})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve(f.ctx)
	return wireguard(t, priv, peer(f.server.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String())), c
}

func TestShapePollRunsBothWays(t *testing.T) {
	ck := key(t)
	s := secret(t)
	shape, _ := stealth.Preset("get-poll")
	f := newShapeFixture(t, shape, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, c := f.client(t, s, ck)

	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 15*time.Second) {
		t.Fatalf("nothing reached the server: %+v", f.gw.Stats())
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 10*time.Second) {
		t.Fatalf("nothing reached the client: %+v", f.gw.Stats())
	}
	if c.Sessions() < 1 {
		t.Fatalf("no session was opened")
	}
}

func TestShapeStreamRunsBothWays(t *testing.T) {
	ck := key(t)
	s := secret(t)
	shape, _ := stealth.Preset("post-stream")
	f := newShapeFixture(t, shape, ck)
	f.gw.SetDevices([]Device{{PublicKey: ck.PublicKey(), Secret: s}})
	cl, c := f.client(t, s, ck)

	if !through(cl, f.server, tuntest.Ping(serverIP, clientIP), 15*time.Second) {
		t.Fatalf("nothing reached the server over the stream: %+v", f.gw.Stats())
	}
	if !through(f.server, cl, tuntest.Ping(clientIP, serverIP), 10*time.Second) {
		t.Fatalf("nothing reached the client over the stream: %+v", f.gw.Stats())
	}
	if c.Sessions() < 1 {
		t.Fatalf("no session was opened")
	}
}
