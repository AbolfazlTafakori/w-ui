package service

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Removing a tunnel waits for its port to be let go, so one made on the same
// port at once is not refused -- and no longer than it takes.
func TestRemovalWaitsForThePortToBeLetGo(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(300 * time.Millisecond)
		l.Close()
	}()
	start := time.Now()
	awaitPortsFree(context.Background(), []int{port}, 5*time.Second)
	took := time.Since(start)
	if took < 250*time.Millisecond || took > 2*time.Second {
		t.Fatalf("waited %s for a port let go after 300ms", took)
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
