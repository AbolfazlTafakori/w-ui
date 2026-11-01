package stealthgw

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// buildFakeHello makes a ClientHello with name as its SNI and a standalone
// X25519 share of x's public key. The carrier bytes are zero until sealed.
func buildFakeHello(t *testing.T, name string, x []byte) []byte {
	t.Helper()
	priv, err := ecdh.X25519().NewPrivateKey(x)
	if err != nil {
		t.Fatal(err)
	}
	share := priv.PublicKey().Bytes()
	ext := func(kind uint16, body []byte) []byte {
		b := make([]byte, 4+len(body))
		binary.BigEndian.PutUint16(b, kind)
		binary.BigEndian.PutUint16(b[2:], uint16(len(body)))
		copy(b[4:], body)
		return b
	}
	nameList := append([]byte{0}, byte(len(name)>>8), byte(len(name)))
	nameList = append(nameList, name...)
	snBody := append([]byte{byte(len(nameList) >> 8), byte(len(nameList))}, nameList...)
	sv := []byte{2, 3, 4}
	ks := []byte{0x00, 0x1d, 0x00, 0x20}
	ks = append(ks, share...)
	ksBody := append([]byte{byte(len(ks) >> 8), byte(len(ks))}, ks...)
	sg := []byte{0, 2, 0, 0x1d}
	exts := bytes.Join([][]byte{ext(0, snBody), ext(43, sv), ext(51, ksBody), ext(10, sg)}, nil)

	var body []byte
	body = append(body, 3, 3)
	body = append(body, make([]byte, 32)...)
	body = append(body, 32)
	body = append(body, make([]byte, 32)...)
	body = append(body, 0, 2, 0x13, 0x01)
	body = append(body, 1, 0)
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)
	msg := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...)
}

// udpEcho is a local UDP socket that sends back every datagram it reads: it
// stands in for the WireGuard tunnel so a session can establish.
func udpEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			c.WriteToUDP(append([]byte(nil), buf[:n]...), from)
		}
	}()
	return c.LocalAddr().(*net.UDPAddr)
}

func TestFakeStreamCarry(t *testing.T) {
	echo := udpEcho(t)
	var priv [32]byte
	rand.Read(priv[:])
	tk, err := stealth.NewTransportKey()
	if err != nil {
		t.Fatal(err)
	}
	g, err := newStreamGateway(StreamConfig{
		Path: "/faketls-path", WireGuard: echo.String(), PrivateKey: priv,
		TransportKeys: []stealth.TransportKey{tk}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, stealth.FakeTLS)
	if err != nil {
		t.Fatal(err)
	}

	// One device, known to the gateway and the client under the same anchor.
	var anchor stealth.Anchor
	copy(anchor[:], g.resp.priv.PublicKey().Bytes())
	var secret stealth.Secret
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	var pub [32]byte
	rand.Read(pub[:])
	g.SetDevices([]Device{{PublicKey: pub, Secret: secret}})
	dev := g.devices[pub]
	devKeys := stealth.Derive(secret, anchor)

	// The transport's faketls key pair (a plain X25519 pair), and the two
	// ends built on it.
	const site = "www.site.example"
	nodePriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tPriv := nodePriv.Bytes()
	tPub := nodePriv.PublicKey().Bytes()
	inbound := g.resp.priv.PublicKey().Bytes()
	dummy := make([]byte, 32)
	rand.Read(dummy)
	node := stealth.NewFakeNode(tPriv, inbound, dummy)
	client := stealth.NewFakeClient(tPub, devKeys, inbound)

	// The client builds and seals a hello.
	x := make([]byte, 32)
	rand.Read(x)
	hello := buildFakeHello(t, site, x)
	if !client.Carry(hello, x, time.Now().Unix()) {
		t.Fatal("the client did not seal the hello")
	}

	// The gateway opens it.
	now := time.Now()
	hour := stealth.FakeHourOf(now.Unix())
	proof := node.Open(hello, [][]byte{[]byte(site)}, func(tag [stealth.TagLen]byte) (any, []byte, bool) {
		if tag == devKeys.HourTag(hour) {
			return dev, devKeys.Bind, true
		}
		return nil, nil, false
	})
	if proof.Kind != stealth.FakeProved {
		t.Fatalf("hello did not prove a device: kind %d", proof.Kind)
	}

	leaf, _ := stealth.LeafKey(proof.Seed)
	h, _ := stealth.ParseHello(hello)
	server, err := stealth.FakeServerFlight(hello, h, stealth.SuiteAES128, leaf)
	if err != nil {
		t.Fatal(err)
	}

	// The connection between the two ends.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	// A place for the session, and the gateway's side of the carry.
	if why := g.reserve(dev, netip.AddrFrom4([4]byte{127, 0, 0, 1}), now); why >= 0 {
		t.Fatalf("no place for the session: %d", why)
	}
	s, ok := g.newFakeSession(dev, netip.AddrFrom4([4]byte{127, 0, 0, 1}), &fakeSizer{}, now)
	if !ok {
		t.Fatal("no session")
	}
	s.conn, s.r = b, bufio.NewReader(b)
	go g.carry(s, server)

	// The client reads the flight, finishes the handshake, and sends a frame
	// with a keepalive -- which opens the tunnel -- and a data datagram.
	ca := bufio.NewReader(a)
	flight := readWholeFlight(t, ca)
	expected := client.Leaf(x, hello)
	clientFin, cc, err := stealth.FakeClientFlight(hello, x, nil, expected, flight)
	if err != nil {
		t.Fatalf("client flight: %v", err)
	}
	if _, err := a.Write(clientFin); err != nil {
		t.Fatal(err)
	}

	keepalive := make([]byte, 32)
	keepalive[0] = 4
	data := make([]byte, 40)
	data[0] = 4
	for i := range data {
		if i >= 4 {
			data[i] = byte(i)
		}
	}
	frame, _ := stealth.AppendFakeFrame(nil, 0, keepalive, data)
	rec, ok := cc.Send.Seal(23, frame, 0, nil)
	if !ok {
		t.Fatal("client seal")
	}
	if _, err := a.Write(rec); err != nil {
		t.Fatal(err)
	}

	// The tunnel echoes the data back; the gateway should carry it to the
	// client inside an application record.
	a.SetReadDeadline(time.Now().Add(8 * time.Second))
	got := readDatagram(t, ca, cc, data)
	if !bytes.Equal(got, data) {
		t.Fatalf("datagram did not round-trip: got %d bytes", len(got))
	}
}

// readWholeFlight reads the server's flight: a plaintext ServerHello record, a
// change_cipher_spec, and the encrypted handshake record.
func readWholeFlight(t *testing.T, r *bufio.Reader) []byte {
	t.Helper()
	var out []byte
	for i := 0; i < 3; i++ {
		_, rec, err := readRecord(r)
		if err != nil {
			t.Fatalf("flight record %d: %v", i, err)
		}
		out = append(out, rec...)
	}
	return out
}

// readDatagram reads application records until a frame carries want.
func readDatagram(t *testing.T, r *bufio.Reader, cc *stealth.FakeConn, want []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		outer, rec, err := readRecord(r)
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		if outer != 23 {
			continue
		}
		kind, content, ok := openRecord13(cc.Recv, rec)
		if !ok || kind != 23 {
			continue
		}
		var found []byte
		stealth.ReadFakeFrames(content, func(d []byte) {
			if bytes.Equal(d, want) {
				found = append([]byte(nil), d...)
			}
		})
		if found != nil {
			return found
		}
	}
	t.Fatal("the datagram never came")
	return nil
}
