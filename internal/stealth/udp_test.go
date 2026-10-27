package stealth

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// room is the largest packet of a tunnel with an MTU of 1390.
const room = 1390 + UDPOverhead

var (
	testSecret = Secret(bytes.Repeat([]byte{0x11}, 32))
	testAnchor = Anchor(bytes.Repeat([]byte{0x22}, 32))
	testNow    = time.Unix(1_700_000_000, 0)
)

// keepalive is a WireGuard keepalive in shape: type 4, then 28 bytes.
func keepalive() []byte { return append([]byte{4, 0, 0, 0}, datagram(28, 9)...) }

// tags is a server's table: the device's tags for the epochs around now.
func tags(k DeviceKeys, now time.Time) map[Tag]bool {
	e := Epoch(now)
	return map[Tag]bool{k.EpochTag(e - 1): true, k.EpochTag(e): true, k.EpochTag(e + 1): true}
}

func openOne(t *testing.T, c *UDPClient, d []byte) ([]byte, UDPKeys) {
	t.Helper()
	pad := NewPadder(0, 0)
	p, keys, err := c.Open(nil, testNow, d, &pad, room)
	if err != nil {
		t.Fatal(err)
	}
	return p, keys
}

func TestAnOpeningStartsASessionBothWays(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	srv := NewUDPServer(testAnchor)
	dev := Derive(testSecret, testAnchor)
	init := append([]byte{1, 0, 0, 0}, datagram(144, 5)...)

	p, ck := openOne(t, c, init)
	tag, ok := srv.Tag(p)
	if !ok || !tags(dev, testNow)[tag] {
		t.Fatal("the opening's tag is not the device's")
	}
	o, err := srv.Unseal(dev, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Timestamp != uint64(testNow.Unix()) || !bytes.Equal(o.Datagram, init) {
		t.Fatalf("opened timestamp %d, datagram %d bytes", o.Timestamp, len(o.Datagram))
	}
	sk := o.Keys()
	if !bytes.Equal(sk.Up, ck.Up) || !bytes.Equal(sk.Down, ck.Down) || bytes.Equal(sk.Up, sk.Down) {
		t.Fatal("the two sides do not hold the same two keys")
	}

	up, down := NewUDPSender(ck.Up, NewPadder(0, 0)), NewUDPSender(sk.Down, NewPadder(0, 0))
	atServer, atClient := NewUDPReceiver(sk.Up), NewUDPReceiver(ck.Down)
	for i := range 40 {
		d := datagram(MinDatagram+i*30, byte(i))
		pk, err := up.Seal(nil, d, false, room)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := atServer.Open(pk, nil); err != nil || !bytes.Equal(got, d) {
			t.Fatalf("packet %d up: %v", i, err)
		}
		if _, err := atClient.Open(pk, nil); err == nil {
			t.Fatal("a packet up opened under the key down")
		}
		pk, err = down.Seal(nil, nil, false, room)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := atClient.Open(pk, nil); err != nil || got != nil {
			t.Fatalf("padding-only packet %d down: %v %v", i, got, err)
		}
	}
	// A fresh opening gives a fresh salt and fresh keys.
	_, again := openOne(t, c, init)
	if bytes.Equal(again.Up, ck.Up) {
		t.Fatal("two openings gave one key")
	}
}

func TestTheTagChangesWithEveryOpeningAndOnlyTheServerOfTheTunnelFindsIt(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	a, _ := openOne(t, c, keepalive())
	b, _ := openOne(t, c, keepalive())
	if bytes.Equal(a[:TagLen], b[:TagLen]) || bytes.Equal(a[TagLen:TagLen+saltLen], b[TagLen:TagLen+saltLen]) {
		t.Fatal("two openings share a masked tag or a salt")
	}
	dev := Derive(testSecret, testAnchor)
	other := NewUDPServer(Anchor(bytes.Repeat([]byte{0x23}, 32)))
	if tag, _ := other.Tag(a); tags(dev, testNow)[tag] {
		t.Fatal("another tunnel's server unmasked the device's tag")
	}
	if tag, _ := NewUDPServer(testAnchor).Tag(a); tags(Derive(Secret(bytes.Repeat([]byte{0x12}, 32)), testAnchor), testNow)[tag] {
		t.Fatal("another device's tag matched")
	}
}

func TestAnOpeningIsKnownOneEpochEitherSideAndNoFurther(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	dev := Derive(testSecret, testAnchor)
	p, _ := openOne(t, c, keepalive())
	tag, _ := NewUDPServer(testAnchor).Tag(p)
	for _, tc := range []struct {
		shift time.Duration
		known bool
	}{{0, true}, {-EpochLength, true}, {EpochLength, true}, {-2 * EpochLength, false}, {2 * EpochLength, false}} {
		if got := tags(dev, testNow.Add(tc.shift))[tag]; got != tc.known {
			t.Errorf("a server clock %v away: known %v, want %v", tc.shift, got, tc.known)
		}
	}
}

func TestEveryChangedBitIsRefused(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	srv := NewUDPServer(testAnchor)
	dev := Derive(testSecret, testAnchor)
	good, keys := openOne(t, c, keepalive())
	table := tags(dev, testNow)
	for bit := range len(good) * 8 {
		p := bytes.Clone(good)
		p[bit/8] ^= 1 << (bit % 8)
		tag, _ := srv.Tag(p)
		if !table[tag] {
			continue // a changed tag or salt names nobody
		}
		if bit < (TagLen+saltLen)*8 {
			t.Fatalf("bit %d: a changed tag or salt still named the device", bit)
		}
		if _, err := srv.Unseal(dev, p, nil); err == nil {
			t.Fatalf("bit %d: a changed opening opened", bit)
		}
	}
	data, _ := NewUDPSender(keys.Up, NewPadder(0, 0)).Seal(nil, keepalive(), false, room)
	r := NewUDPReceiver(keys.Up)
	for bit := range len(data) * 8 {
		p := bytes.Clone(data)
		p[bit/8] ^= 1 << (bit % 8)
		if _, err := r.Open(p, nil); err == nil {
			t.Fatalf("bit %d: a changed data packet opened", bit)
		}
	}
	if r.Received() != 0 {
		t.Fatal("a refused packet was counted")
	}
}

// forge seals an opening whose header and chunk are whatever the test says.
func forge(dev DeviceKeys, anchor Anchor, header, chunk []byte) []byte {
	var salt [saltLen]byte
	salt[0] = 7
	tag := dev.EpochTag(Epoch(testNow))
	m := mask(MaskKey(anchor), salt[:])
	var p []byte
	for i := range tag {
		p = append(p, tag[i]^m[i])
	}
	p = append(p, salt[:]...)
	plain := append(bytes.Clone(header), chunk...)
	return newAEAD(dev.salted(salt[:]).key("open")).Seal(p, make([]byte, udpNonce), plain, p[:TagLen+saltLen])
}

func TestAnOpeningHoldsOneDatagramBehindTheVersionAndZeros(t *testing.T) {
	dev := Derive(testSecret, testAnchor)
	srv := NewUDPServer(testAnchor)
	header := func(v, z byte) []byte {
		h := []byte{v, z, 0, 0}
		return binary.BigEndian.AppendUint64(h, uint64(testNow.Unix()))
	}
	one, _ := AppendItem(nil, keepalive())
	two, _ := AppendItem(bytes.Clone(one), keepalive())
	for _, tc := range []struct {
		what          string
		header, chunk []byte
		ok            bool
	}{
		{"the control", header(Version, 0), one, true},
		{"padding after the datagram", header(Version, 0), append(bytes.Clone(one), 0, 0, 0), true},
		{"version 2", header(2, 0), one, false},
		{"version 0", header(0, 0), one, false},
		{"a zero byte that is set", header(Version, 1), one, false},
		{"no datagram", header(Version, 0), make([]byte, 40), false},
		{"an empty chunk", header(Version, 0), nil, false},
		{"two datagrams", header(Version, 0), two, false},
		{"a datagram of 31 bytes", header(Version, 0), append([]byte{0, 31}, datagram(31, 1)...), false},
		{"a length past the end", header(Version, 0), append([]byte{0, 40}, datagram(39, 1)...), false},
		{"padding that is not zero", header(Version, 0), append(bytes.Clone(one), 0, 1), false},
	} {
		p := forge(dev, testAnchor, tc.header, tc.chunk)
		_, err := srv.Unseal(dev, p, nil)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.what, err)
		}
	}
	if _, ok := srv.Tag(make([]byte, OpeningMin-1)); ok {
		t.Error("a datagram too short for an opening named a tag")
	}
	if _, ok := srv.Tag(make([]byte, OpeningMin+MaxPlain+1)); ok {
		t.Error("a datagram too long for an opening named a tag")
	}
}

func TestAnOpeningHasRoomForTheFirstAnswerAndFitsThePath(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	for range 200 {
		p, _ := openOne(t, c, keepalive())
		if len(p) < FirstAnswer || len(p) > room {
			t.Fatalf("an opening of %d bytes", len(p))
		}
	}
	// A path too narrow for the floor still gets an opening that fits it.
	pad := NewPadder(0, 0)
	p, _, err := c.Open(nil, testNow, keepalive(), &pad, 115)
	if err != nil || len(p) != 115 {
		t.Fatalf("an opening of %d bytes on a path of 115: %v", len(p), err)
	}
	for _, tc := range []struct {
		d    []byte
		room int
	}{{datagram(31, 1), room}, {datagram(1601, 1), 9000}, {datagram(148, 1), OpeningMin + 149}, {keepalive(), 0}} {
		out := []byte{0xee}
		out, _, err := c.Open(out, testNow, tc.d, &pad, tc.room)
		if err == nil || !bytes.Equal(out, []byte{0xee}) {
			t.Errorf("a datagram of %d bytes on a path of %d: %v, %d bytes left", len(tc.d), tc.room, err, len(out))
		}
	}
	if FirstAnswer != 122 || UDPOverhead != 62 || OpeningMin != 76 || DataMin != 28 {
		t.Fatal("the sizes of the specification changed")
	}
}

func TestADataPacketFitsItsRoomAndTheFirstSixteenAreWide(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 32)
	s, r := NewUDPSender(key, NewPadder(0, 0)), NewUDPReceiver(key)
	// A full-size inner packet carries no padding.
	full := datagram(1390+32, 7)
	p, err := s.Seal(nil, full, true, room)
	if err != nil || len(p) != room {
		t.Fatalf("a full-size packet of %d bytes: %v", len(p), err)
	}
	if got, _ := r.Open(p, nil); !bytes.Equal(got, full) {
		t.Fatal("the full-size packet did not open")
	}
	// A recorded packet opens again: WireGuard, not the wrapping, refuses it.
	if got, _ := r.Open(p, nil); !bytes.Equal(got, full) || r.Received() != 2 {
		t.Fatal("a repeated packet did not open")
	}
	if _, err := s.Seal(nil, full, false, room-1); !errors.Is(err, ErrSize) {
		t.Fatalf("a packet past its room: %v", err)
	}
	if p, err := s.Seal(nil, nil, false, DataMin); err != nil || len(p) != DataMin {
		t.Fatalf("the smallest packet: %d bytes, %v", len(p), err)
	}
	if _, err := s.Seal(nil, nil, false, DataMin-1); !errors.Is(err, ErrSize) {
		t.Fatalf("a room below the smallest packet: %v", err)
	}

	// Over many sessions, the first sixteen data packets reach past the
	// normal ceiling and the ones after never do.
	var early, late int
	for range 50 {
		s := NewUDPSender(key, NewPadder(0, 0))
		for i := range 32 {
			p, _ := s.Seal(nil, keepalive(), false, room)
			pad := len(p) - DataMin - 2 - 32
			if i < wideChunks {
				early = max(early, pad)
			} else {
				late = max(late, pad)
			}
		}
	}
	if early <= NormalMax || late > NormalMax {
		t.Fatalf("widest early padding %d, widest late %d", early, late)
	}
}

func TestADataPacketHoldsOneDatagramOrNone(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 32)
	one, _ := AppendItem(nil, keepalive())
	two, _ := AppendItem(bytes.Clone(one), keepalive())
	for _, tc := range []struct {
		plain []byte
		ok    bool
	}{{nil, true}, {make([]byte, 9), true}, {one, true}, {two, false}, {append([]byte{0, 31}, datagram(31, 1)...), false}} {
		nonce := bytes.Repeat([]byte{0x10}, udpNonce)
		p := newAEAD(key).Seal(bytes.Clone(nonce), nonce, tc.plain, nil)
		if _, err := NewUDPReceiver(key).Open(p, nil); (err == nil) != tc.ok {
			t.Errorf("a chunk of %d bytes: %v", len(tc.plain), err)
		}
	}
}

func TestAKeySealsNoMoreThanItsLimit(t *testing.T) {
	s := NewUDPSender(bytes.Repeat([]byte{0x33}, 32), NewPadder(0, 0))
	s.sent = KeyPackets - 1
	if _, err := s.Seal(nil, nil, false, room); err != nil {
		t.Fatal(err)
	}
	out := []byte{1}
	out, err := s.Seal(out, keepalive(), false, room)
	if !errors.Is(err, ErrNumber) || len(out) != 1 || s.Sent() != KeyPackets {
		t.Fatalf("past the limit: %v, %d bytes, %d sent", err, len(out), s.Sent())
	}
}

func TestSealingAppendsToWhatIsThere(t *testing.T) {
	c := NewUDPClient(testSecret, testAnchor)
	pad := NewPadder(0, 0)
	prefix := []byte("prefix")
	p, keys, err := c.Open(bytes.Clone(prefix), testNow, keepalive(), &pad, room)
	if err != nil || !bytes.HasPrefix(p, prefix) {
		t.Fatal("the opening did not append")
	}
	if _, err := NewUDPServer(testAnchor).Unseal(Derive(testSecret, testAnchor), p[len(prefix):], nil); err != nil {
		t.Fatal("an appended opening did not open")
	}
	d, err := NewUDPSender(keys.Up, NewPadder(0, 0)).Seal(bytes.Clone(prefix), keepalive(), false, room)
	if err != nil || !bytes.HasPrefix(d, prefix) {
		t.Fatal("the data packet did not append")
	}
	if _, err := NewUDPReceiver(keys.Up).Open(d[len(prefix):], nil); err != nil {
		t.Fatal("an appended data packet did not open")
	}
}
