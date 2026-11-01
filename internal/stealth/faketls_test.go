package stealth

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildHello makes a ClientHello the gateway reads, with name as its SNI and a
// standalone X25519 share of x25519Base(x). The random and session id are
// zero until a client seals them.
func buildHello(name string, x []byte) []byte {
	share := x25519Base(x)
	ext := func(kind uint16, body []byte) []byte {
		b := make([]byte, 4+len(body))
		binary.BigEndian.PutUint16(b, kind)
		binary.BigEndian.PutUint16(b[2:], uint16(len(body)))
		copy(b[4:], body)
		return b
	}
	// server_name
	nameList := append([]byte{0}, byte(len(name)>>8), byte(len(name)))
	nameList = append(nameList, name...)
	snBody := append([]byte{byte(len(nameList) >> 8), byte(len(nameList))}, nameList...)
	// supported_versions: just TLS 1.3
	sv := []byte{2, 3, 4}
	// key_share: one standalone X25519 value
	ks := []byte{0x00, 0x1d, 0x00, 0x20}
	ks = append(ks, share...)
	ksBody := append([]byte{byte(len(ks) >> 8), byte(len(ks))}, ks...)
	// supported_groups
	sg := []byte{0, 2, 0, 0x1d}

	exts := bytes.Join([][]byte{
		ext(0, snBody), ext(43, sv), ext(51, ksBody), ext(10, sg),
	}, nil)

	var body []byte
	body = append(body, 3, 3)                // legacy_version
	body = append(body, make([]byte, 32)...) // random
	body = append(body, 32)                  // session id length
	body = append(body, make([]byte, 32)...) // session id
	body = append(body, 0, 2, 0x13, 0x01)    // cipher suites
	body = append(body, 1, 0)                // compression
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	msg := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...)
}

func fakeSetup(t *testing.T) (secret Secret, anchor Anchor, tPriv, tPub []byte) {
	t.Helper()
	for i := range secret {
		secret[i], anchor[i] = byte(i), byte(0x20+i)
	}
	// The transport key is an X25519 key pair; a fixed scalar is enough here.
	tPriv = make([]byte, 32)
	for i := range tPriv {
		tPriv[i] = byte(0x40 + i)
	}
	tPub = x25519Base(tPriv)
	return
}

func TestFakeProofRoundTrip(t *testing.T) {
	secret, anchor, tPriv, tPub := fakeSetup(t)
	const site = "www.site.example"
	now := int64(1_700_000_000)
	x := make([]byte, 32)
	for i := range x {
		x[i] = byte(0x90 + i)
	}
	msg := buildHello(site, x)

	client := NewFakeClient(tPub, Derive(secret, anchor), anchor[:])
	if !client.Carry(msg, x, now) {
		t.Fatal("Carry refused a good hello")
	}

	keys := Derive(secret, anchor)
	find := func(tag [TagLen]byte) (any, []byte, bool) {
		for _, h := range []uint64{FakeHourOf(now), FakeHourOf(now) - 1, FakeHourOf(now) + 1} {
			if keys.HourTag(h) == tag {
				return "dev-1", keys.Bind, true
			}
		}
		return nil, nil, false
	}
	node := NewFakeNode(tPriv, anchor[:], bytes.Repeat([]byte{7}, 32))
	out := node.Open(msg, [][]byte{[]byte(site)}, find)
	if out.Kind != FakeProved {
		t.Fatalf("a good proof was %v, not proved", out.Kind)
	}
	if out.Device != "dev-1" || out.Timestamp != uint64(now) {
		t.Fatalf("proof carried %v / %d", out.Device, out.Timestamp)
	}

	// The leaf the client expects is the one the node's seed makes.
	_, want := LeafKey(out.Seed)
	if got := client.Leaf(x, msg); !bytes.Equal(got, want) {
		t.Fatal("the client's expected leaf is not the node's")
	}
}

func TestFakeStrangerAndRefused(t *testing.T) {
	secret, anchor, tPriv, tPub := fakeSetup(t)
	const site = "www.site.example"
	now := int64(1_700_000_000)
	x := bytes.Repeat([]byte{0x11}, 32)
	keys := Derive(secret, anchor)
	node := NewFakeNode(tPriv, anchor[:], bytes.Repeat([]byte{7}, 32))
	find := func(tag [TagLen]byte) (any, []byte, bool) {
		if keys.HourTag(FakeHourOf(now)) == tag {
			return "dev", keys.Bind, true
		}
		return nil, nil, false
	}

	// An unsealed hello is a stranger: its carrier is zero, so it opens as
	// nothing.
	plain := buildHello(site, x)
	if out := node.Open(plain, [][]byte{[]byte(site)}, find); out.Kind != FakeStranger {
		t.Fatalf("an unsealed hello was %v, not a stranger", out.Kind)
	}

	// A sealed hello for a site the node does not serve is a stranger.
	client := NewFakeClient(tPub, keys, anchor[:])
	other := buildHello(site, x)
	client.Carry(other, x, now)
	if out := node.Open(other, [][]byte{[]byte("not.the.site")}, find); out.Kind != FakeStranger {
		t.Fatalf("a hello for another site was %v, not a stranger", out.Kind)
	}

	// A sealed hello whose device the table does not hold is refused: the
	// carrier opens, but the binder is checked with a dummy key.
	noDevice := buildHello(site, x)
	client.Carry(noDevice, x, now)
	none := func(tag [TagLen]byte) (any, []byte, bool) { return nil, nil, false }
	if out := node.Open(noDevice, [][]byte{[]byte(site)}, none); out.Kind != FakeRefused {
		t.Fatalf("an unknown device was %v, not refused", out.Kind)
	}
}

func TestFakeHelloRoundTrip(t *testing.T) {
	secret, anchor, tPriv, tPub := fakeSetup(t)
	const site = "cdn.example.net"
	now := int64(1_700_000_000)
	x := make([]byte, 32)
	for i := range x {
		x[i] = byte(0x11 + i)
	}
	hello, ok := FakeHello(site, x)
	if !ok {
		t.Fatal("FakeHello did not build")
	}
	h, ok := ParseHello(hello)
	if !ok || !h.TLS13 || string(h.Name) != site {
		t.Fatalf("the built hello did not parse: ok=%v name=%q tls13=%v", ok, h.Name, h.TLS13)
	}
	keys := Derive(secret, anchor)
	inbound := x25519Base(make([]byte, 32)) // any 32-byte inbound; the same both sides
	client := NewFakeClient(tPub, keys, inbound)
	if !client.Carry(hello, x, now) {
		t.Fatal("the client did not seal the built hello")
	}
	node := NewFakeNode(tPriv, inbound, []byte("0123456789abcdef0123456789abcdef"))
	hour := FakeHourOf(now)
	proof := node.Open(hello, [][]byte{[]byte(site)}, func(tag [TagLen]byte) (any, []byte, bool) {
		if tag == keys.HourTag(hour) {
			return "dev", keys.Bind, true
		}
		return nil, nil, false
	})
	if proof.Kind != FakeProved {
		t.Fatalf("the built hello did not prove: kind %d", proof.Kind)
	}
}
