package stealthgw

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// TestFakeTLSClientRoundTrip runs the reference client against the gateway: a
// stand-in WireGuard sends a keepalive to the client, which opens a session,
// and the gateway's looped-back tunnel echoes it back to the WireGuard.
func TestFakeTLSClientRoundTrip(t *testing.T) {
	saw := make(chan []byte, 1)
	site := fakeSite(t, []byte("unused"), saw)
	g, node, anchor := newFrontGateway(t, site)

	var secret stealth.Secret
	for i := range secret {
		secret[i] = byte(i + 3)
	}
	var pub [32]byte
	rand.Read(pub[:])
	g.SetDevices([]Device{{PublicKey: pub, Secret: secret}})

	nodePriv, _ := ecdh.X25519().NewPrivateKey(node[:])
	nodePub := base64.StdEncoding.EncodeToString(nodePriv.PublicKey().Bytes())
	host, _, _ := net.SplitHostPort(site)

	client, err := NewFakeTLSClient(FakeTLSClientConfig{
		Dial: g.Addr().String(), Name: host, NodeKey: nodePub,
		Listen: "127.0.0.1:0", Secret: secret, Anchor: anchor,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)

	// A stand-in WireGuard that sends keepalives to the client and waits for
	// one to come back through the tunnel.
	wg, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer wg.Close()
	clientAddr := client.LocalAddr().(*net.UDPAddr)

	keepalive := make([]byte, 32)
	keepalive[0] = 4
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(400 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				wg.WriteToUDP(keepalive, clientAddr)
			}
		}
	}()

	wg.SetReadDeadline(time.Now().Add(12 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := wg.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("nothing came back through the tunnel: %v", err)
	}
	if n != 32 || buf[0] != 4 {
		t.Fatalf("what came back is not the keepalive: %d bytes", n)
	}
	if client.Sessions() == 0 {
		t.Fatal("the client opened no session")
	}
}
