package stealth

import (
	"bytes"
	"crypto/mlkem"
	"testing"
)

func TestFakeHandshakeRoundTrip(t *testing.T) {
	for _, suite := range []Suite{SuiteAES128, SuiteAES256, SuiteChacha} {
		x := bytes.Repeat([]byte{0x33}, 32)
		hello := buildHello("www.site.example", x)
		h, ok := ParseHello(hello)
		if !ok {
			t.Fatal("hello did not parse")
		}

		var seed [40]byte
		for i := range seed {
			seed[i] = byte(i + 1)
		}
		leafPriv, leafPub := LeafKey(seed)

		server, err := FakeServerFlight(hello, h, suite, leafPriv)
		if err != nil {
			t.Fatalf("suite %d: server flight: %v", suite, err)
		}
		clientFin, clientConn, err := FakeClientFlight(hello, x, nil, leafPub, server.Flight)
		if err != nil {
			t.Fatalf("suite %d: client flight: %v", suite, err)
		}
		serverConn, err := server.AcceptClientFinished(clientFin)
		if err != nil {
			t.Fatalf("suite %d: accept client finished: %v", suite, err)
		}

		// Application data each way, under the keys both sides derived.
		rec, ok := serverConn.Send.Seal(tlsAppData, []byte("down"), 3, nil)
		if !ok {
			t.Fatalf("suite %d: server app seal", suite)
		}
		kind, got, ok := openRecord(clientConn.Recv, rec)
		if !ok || kind != tlsAppData || string(got) != "down" {
			t.Fatalf("suite %d: client could not read server data: %q %v", suite, got, ok)
		}
		rec, _ = clientConn.Send.Seal(tlsAppData, []byte("up"), 0, nil)
		kind, got, ok = openRecord(serverConn.Recv, rec)
		if !ok || kind != tlsAppData || string(got) != "up" {
			t.Fatalf("suite %d: server could not read client data: %q %v", suite, got, ok)
		}
	}
}

func TestFakeHandshakeRejectsWrongLeaf(t *testing.T) {
	x := bytes.Repeat([]byte{0x44}, 32)
	hello := buildHello("www.site.example", x)
	h, _ := ParseHello(hello)
	var seed [40]byte
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	leafPriv, _ := LeafKey(seed)
	server, err := FakeServerFlight(hello, h, SuiteAES128, leafPriv)
	if err != nil {
		t.Fatal(err)
	}
	// The client expects a different leaf and must refuse the flight.
	var other [40]byte
	for i := range other {
		other[i] = byte(0xA0 + i)
	}
	_, wrongPub := LeafKey(other)
	if _, _, err := FakeClientFlight(hello, x, nil, wrongPub, server.Flight); err == nil {
		t.Fatal("the client took a flight whose leaf it did not expect")
	}
}

func TestFakeFlightHybrid(t *testing.T) {
	// A hybrid X25519MLKEM768 hello carries an ML-KEM encapsulation key before
	// the X25519 share; the flight answers with the hybrid group and both ends
	// reach the same keys.
	x := make([]byte, 32)
	for i := range x {
		x[i] = byte(0x40 + i)
	}
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		t.Fatal(err)
	}
	hello, ok := FakeHelloHybrid("site.example", x, dk.EncapsulationKey().Bytes())
	if !ok {
		t.Fatal("hybrid hello did not build")
	}
	h, ok := ParseHello(hello)
	if !ok || h.Group != GroupHybrid {
		t.Fatalf("hybrid hello did not parse as hybrid: ok=%v group=%#x", ok, h.Group)
	}
	leaf, leafPub := LeafKey([40]byte{1, 2, 3, 4, 5})
	server, err := FakeServerFlight(hello, h, SuiteChacha, leaf)
	if err != nil {
		t.Fatalf("server flight: %v", err)
	}
	clientFin, clientConn, err := FakeClientFlight(hello, x, dk.Bytes(), leafPub, server.Flight)
	if err != nil {
		t.Fatalf("client flight: %v", err)
	}
	serverConn, err := server.AcceptClientFinished(clientFin)
	if err != nil {
		t.Fatalf("accept client finished: %v", err)
	}
	// The application keys match: seal one way, open the other.
	rec, ok := serverConn.Send.Seal(23, []byte("hybrid hi"), 0, nil)
	if !ok {
		t.Fatal("server seal")
	}
	kind, msg, ok := openRecord(clientConn.Recv, rec)
	if !ok || kind != 23 || string(msg) != "hybrid hi" {
		t.Fatalf("client did not open the server's record: ok=%v kind=%d msg=%q", ok, kind, msg)
	}
}
