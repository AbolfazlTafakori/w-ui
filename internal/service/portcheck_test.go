package service

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Removing a tunnel waits for its port to be let go, so one made on the same
// port at once is not refused -- and no longer than it takes.
func TestRemovalWaitsForThePortToBeLetGo(t *testing.T) {
	// The port is held on TCP and UDP both, as a tunnel's is: the wait
	// looks at both, and a UDP socket of some other test that happened to
	// have the same number made it wait the whole bound, now and then.
	l, c := holdBoth(t)
	port := l.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(300 * time.Millisecond)
		l.Close()
		c.Close()
	}()
	tcpAt, tcpWhy := probePort(port, "tcp")
	udpAt, udpWhy := probePort(port, "udp")
	start := time.Now()
	awaitPortsFree(context.Background(), []int{port}, 5*time.Second)
	took := time.Since(start)
	if took < 250*time.Millisecond || took > 2*time.Second {
		t.Fatalf("waited %s for a port let go after 300ms; at the start the port read tcp %v (%s), udp %v (%s)",
			took, tcpAt, tcpWhy, udpAt, udpWhy)
	}
	if st, _ := probePort(port, "tcp"); st != portFree {
		t.Fatal("returned while the port was still held")
	}
}

// A port something else keeps is waited on for the bound and no longer.
func TestRemovalDoesNotWaitForeverOnAPortKept(t *testing.T) {
	c, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	awaitPortsFree(context.Background(), []int{c.LocalAddr().(*net.UDPAddr).Port}, 300*time.Millisecond)
	if took := time.Since(start); took < 300*time.Millisecond || took > 2*time.Second {
		t.Fatalf("waited %s, not the bound", took)
	}
}

// A tunnel's ports are its own and its stealth transport's.
func TestTunnelPortsAreItsOwnAndItsStealthTransports(t *testing.T) {
	iface := &model.Interface{ListenPort: 51820, Stealth: model.JSON(model.StealthParams{Transport: "anchor", Port: 443})}
	if got := tunnelPorts(iface); len(got) != 2 || got[0] != 51820 || got[1] != 443 {
		t.Fatalf("ports = %v", got)
	}
	if got := tunnelPorts(&model.Interface{ListenPort: 1194}); len(got) != 1 {
		t.Fatalf("ports = %v", got)
	}
}

// holdBoth takes one port number on TCP and on UDP at once.
//
// The number is the one the system gives a UDP socket, then taken on TCP
// too. The other way round failed now and then on Windows: a number it
// hands out for TCP can lie in a range it keeps back for UDP (Hyper-V and
// WinNAT reserve ranges per protocol), so the UDP half could not be had
// and the wait looked at a port that was never the test's.
func holdBoth(t *testing.T) (net.Listener, net.PacketConn) {
	t.Helper()
	var last error
	for i := 0; i < 200; i++ {
		c, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		port := c.LocalAddr().(*net.UDPAddr).Port
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			return l, c
		}
		last = err
		c.Close() // that number is not to be had on TCP: another
	}
	t.Fatalf("no port number free on both TCP and UDP; the last refusal: %v", last)
	return nil, nil
}
