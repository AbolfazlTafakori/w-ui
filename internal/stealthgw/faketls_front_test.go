package stealthgw

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// fakeSite is a stand-in for the borrowed site: it reads a connection's first
// bytes and writes back one TLS-looking application record carrying marker, so
// a relayed visitor gets an answer and the shaper has a size to learn.
func fakeSite(t *testing.T, marker []byte, saw chan<- []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				select {
				case saw <- append([]byte(nil), buf[:n]...):
				default:
				}
				// A record: type 23, version, length, then marker as the body.
				rec := []byte{23, 3, 3, byte(len(marker) >> 8), byte(len(marker))}
				rec = append(rec, marker...)
				c.Write(rec)
				io.Copy(io.Discard, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// newFrontGateway starts a wui-faketls gateway with a looped-back tunnel and a
// replay file low enough that fresh proofs are admitted at once.
func newFrontGateway(t *testing.T, site string) (*FakeTLSGateway, [32]byte, stealth.Anchor) {
	t.Helper()
	echo := udpEcho(t)
	var priv [32]byte
	rand.Read(priv[:])
	var node [32]byte
	rand.Read(node[:])

	replayPath := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(replayPath, uint64(time.Now().Unix())-100); err != nil {
		t.Fatal(err)
	}
	g, err := NewFakeTLS(FakeTLSConfig{
		Listen: "127.0.0.1:0", Site: site, NodeKey: node,
		WireGuard: echo.String(), PrivateKey: priv, ReplayFile: replayPath,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.Serve(ctx)

	var anchor stealth.Anchor
	copy(anchor[:], g.resp.priv.PublicKey().Bytes())
	return g, node, anchor
}

func TestFakeTLSRelaysStrangers(t *testing.T) {
	saw := make(chan []byte, 1)
	marker := []byte("SITE-HELLO-PAGE")
	site := fakeSite(t, marker, saw)
	g, _, _ := newFrontGateway(t, site)

	c, err := net.Dial("tcp", g.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// A visitor: a well-formed ClientHello for a name the gateway does not own.
	x := make([]byte, 32)
	rand.Read(x)
	hello := buildFakeHello(t, "other.example", x)
	rec := append([]byte{22, 3, 3, byte(len(hello) >> 8), byte(len(hello))}, hello...)
	if _, err := c.Write(rec); err != nil {
		t.Fatal(err)
	}

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(marker)+5)
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("no answer from the site through the relay: %v", err)
	}
	if !bytes.Contains(got, marker) {
		t.Fatalf("the site's answer did not come back: %q", got)
	}
	select {
	case b := <-saw:
		if !bytes.Contains(b, []byte("other.example")) {
			t.Fatal("the site did not receive the visitor's hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the site saw nothing")
	}
}

func TestFakeTLSProvesDevice(t *testing.T) {
	saw := make(chan []byte, 1)
	site := fakeSite(t, []byte("unused"), saw)
	g, node, anchor := newFrontGateway(t, site)

	// One device, known to the gateway and the client under the same anchor.
	var secret stealth.Secret
	for i := range secret {
		secret[i] = byte(i + 7)
	}
	var pub [32]byte
	rand.Read(pub[:])
	g.SetDevices([]Device{{PublicKey: pub, Secret: secret}})
	devKeys := stealth.Derive(secret, anchor)

	// The client's side, built on the gateway's public node key.
	nodePriv, _ := ecdh.X25519().NewPrivateKey(node[:])
	nodePub := nodePriv.PublicKey().Bytes()
	inbound := g.resp.priv.PublicKey().Bytes()
	host, _, _ := net.SplitHostPort(site)
	client := stealth.NewFakeClient(nodePub, devKeys, inbound)

	x := make([]byte, 32)
	rand.Read(x)
	hello := buildFakeHello(t, host, x)
	if !client.Carry(hello, x, time.Now().Unix()) {
		t.Fatal("the client did not seal the hello")
	}

	c, err := net.Dial("tcp", g.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rec := append([]byte{22, 3, 3, byte(len(hello) >> 8), byte(len(hello))}, hello...)
	if _, err := c.Write(rec); err != nil {
		t.Fatal(err)
	}

	c.SetReadDeadline(time.Now().Add(8 * time.Second))
	r := bufio.NewReader(c)
	flight := readWholeFlight(t, r)
	expected := client.Leaf(x, hello)
	clientFin, cc, err := stealth.FakeClientFlight(hello, x, nil, expected, flight)
	if err != nil {
		t.Fatalf("client flight: %v", err)
	}
	if _, err := c.Write(clientFin); err != nil {
		t.Fatal(err)
	}

	keepalive := make([]byte, 32)
	keepalive[0] = 4
	data := make([]byte, 48)
	data[0] = 4
	for i := 4; i < len(data); i++ {
		data[i] = byte(i)
	}
	frame, _ := stealth.AppendFakeFrame(nil, 0, keepalive, data)
	sealed, ok := cc.Send.Seal(23, frame, 0, nil)
	if !ok {
		t.Fatal("client seal")
	}
	if _, err := c.Write(sealed); err != nil {
		t.Fatal(err)
	}

	got := readDatagram(t, r, cc, data)
	if !bytes.Equal(got, data) {
		t.Fatalf("datagram did not round-trip: %d bytes", len(got))
	}
}
