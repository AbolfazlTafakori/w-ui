package stealth

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"math/big"

	"crypto/hkdf"
	"golang.org/x/crypto/chacha20poly1305"
)

// The wui-faketls proof.
//
// wui-faketls is the borrowed-site mode: a device opens a session by sending
// a real-looking TLS 1.3 ClientHello whose SNI is a site the operator chose,
// and hides its proof in the 64 bytes a ClientHello spends on random (32) and
// its legacy session id (32). A connection that proves nothing is relayed to
// the real site byte for byte, so a probe gets the site's own certificate; a
// connection that proves a device's secret is answered by the gateway's own
// TLS 1.3 handshake, written by hand with a throwaway leaf whose key only the
// two ends can compute.
//
// This file is the proof alone -- reading the hello, the carrier the proof
// rides in, and the throwaway leaf's key. The hand-written handshake, the
// shaping of its records after the site's, and the relay are the gateway's.
//
//	carrier    = ChaCha20-Poly1305(k_seal, plaintext, nonce 0, aad = label | hello with the carrier zeroed)
//	plaintext  = 0x01 | 0 0 0 | timestamp(8) | hour tag(16) | binder(16) | 0 0 0 0      (48 bytes)
//	binder     = HMAC-SHA-256(k_bind, b | plaintext[0:28])[0:16],  b = Expand(prk_f, "faketls binder" | X)
//	k_seal     = Expand(prk_f, "faketls seal" | X | T_pub | inbound_pub)
//	prk_f      = HKDF-Extract(salt "w-ui stealth v1 faketls", ikm = X25519(x, T))
//	node seed  = Expand(prk_f, "faketls node" | X | carrier),  the throwaway leaf's key
//
// X is the standalone X25519 share of the hello (or the X25519 part of its
// X25519MLKEM768 hybrid share), x the ephemeral behind it, and T the
// transport's key.

// wui-faketls sizes and constants.
const (
	// FakeHour is how long a proof's hour tag stays good.
	FakeHour = 3600
	// fakeCarrier is the 64 bytes a proof rides in: the random and the
	// legacy session id of the ClientHello.
	fakeCarrier = 64
	// fakePlain is the proof plaintext, before its seal.
	fakePlain = 48
	// fakeVersion is the first byte of the plaintext.
	fakeVersion = 1
	// offsets of the two carrier fields in a ClientHello message.
	fakeRandomAt  = 6  // the header (4) and legacy_version (2)
	fakeSessionAt = 39 // the length byte at 38, the id from 39
)

// fakeDummyShare is what the key agreement runs on when a hello has no share,
// so a hello that is no proof costs the same work as one that is.
var fakeDummyShare = [32]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}

// FakeHourOf is the hour of a Unix time: the time divided by 3,600.
func FakeHourOf(now int64) uint64 { return uint64(now) / FakeHour }

// Hello is what the gateway reads of a ClientHello: the site it asks for,
// whether it offers TLS 1.3, and where the X25519 share X lies in the
// message.
type Hello struct {
	Name  []byte
	TLS13 bool
	// Share is the offset of the 32-byte X25519 value the proof is keyed to:
	// the standalone share, or the X25519 part of the hybrid share.
	Share int
	// Group is the key-exchange group the flight answers with: GroupX25519
	// for a standalone share, GroupHybrid for an X25519MLKEM768 one. The proof
	// is keyed to the X25519 part either way.
	Group uint16
}

func be16(b []byte, at int) (int, bool) {
	if at < 0 || at+2 > len(b) {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(b[at:])), true
}

// hostName is the first host_name of a server_name extension body.
func hostName(body []byte) []byte {
	n, ok := be16(body, 0)
	if !ok || n != len(body)-2 {
		return nil
	}
	list := body[2:]
	for at := 0; at < len(list); {
		if at+3 > len(list) {
			return nil
		}
		size, _ := be16(list, at+1)
		if at+3+size > len(list) {
			return nil
		}
		if list[at] == 0 {
			return list[at+3 : at+3+size]
		}
		at += 3 + size
	}
	return nil
}

func offersTLS13(body []byte) bool {
	if len(body) == 0 || int(body[0]) != len(body)-1 || (len(body)-1)%2 != 0 {
		return false
	}
	for at := 1; at+1 < len(body); at += 2 {
		if body[at] == 3 && body[at+1] == 4 {
			return true
		}
	}
	return false
}

// keyShare returns the offsets, in the whole message, of the standalone
// X25519 value and of the X25519 part after the ML-KEM-768 key of the hybrid
// group, for a key_share extension whose body begins at start.
func keyShare(body []byte, start int) (standalone, hybrid int) {
	standalone, hybrid = -1, -1
	n, ok := be16(body, 0)
	if !ok || n != len(body)-2 {
		return -1, -1
	}
	list := body[2:]
	for at := 0; at+4 <= len(list); {
		group, _ := be16(list, at)
		size, _ := be16(list, at+2)
		value := at + 4
		if value+size > len(list) {
			return -1, -1
		}
		switch {
		case group == 0x001d && size == 32 && standalone < 0:
			standalone = start + 2 + value
		case group == 0x11ec && size == 1216 && hybrid < 0:
			hybrid = start + 2 + value + 1184
		}
		at = value + size
	}
	return standalone, hybrid
}

// ParseHello reads a ClientHello handshake message with its 4-byte header.
// Nothing is read past the message, and a message that is not exactly one
// ClientHello, or that lacks a server name, a TLS 1.3 offer or a share, is
// ok=false.
func ParseHello(message []byte) (Hello, bool) {
	if len(message) < 39 || message[0] != 1 {
		return Hello{}, false
	}
	length := int(message[1])<<16 | int(message[2])<<8 | int(message[3])
	if len(message) != 4+length || message[38] != 32 {
		return Hello{}, false
	}
	// Past the fixed head: cipher suites at 71, then compression, then the
	// extensions block.
	n, ok := be16(message, 71)
	if !ok {
		return Hello{}, false
	}
	at := 71 + 2 + n
	if at >= len(message) {
		return Hello{}, false
	}
	at += 1 + int(message[at])
	extLen, ok := be16(message, at)
	if !ok {
		return Hello{}, false
	}
	end := at + 2 + extLen
	if end != len(message) {
		return Hello{}, false
	}
	at += 2
	var (
		name            []byte
		tls13           bool
		standalone      = -1
		hybrid          = -1
		haveName, haveS bool
	)
	for at < end {
		kind, ok1 := be16(message, at)
		size, ok2 := be16(message, at+2)
		start := at + 4
		if !ok1 || !ok2 || start+size > len(message) {
			return Hello{}, false
		}
		body := message[start : start+size]
		switch kind {
		case 0:
			if !haveName {
				name = hostName(body)
				if name == nil {
					return Hello{}, false
				}
				haveName = true
			}
		case 43:
			tls13 = tls13 || offersTLS13(body)
		case 51:
			if !haveS {
				standalone, hybrid = keyShare(body, start)
				if standalone < 0 && hybrid < 0 {
					return Hello{}, false
				}
				haveS = true
			}
		}
		at = start + size
	}
	if !haveName || !haveS {
		return Hello{}, false
	}
	share, group := standalone, GroupX25519
	if share < 0 {
		share, group = hybrid, GroupHybrid
	}
	return Hello{Name: name, TLS13: tls13, Share: share, Group: group}, true
}

// fakeCarrierBytes is the 64 bytes of the carrier: the random, then the
// legacy session id. Called only on a message ParseHello has read.
func fakeCarrierBytes(message []byte) [fakeCarrier]byte {
	var c [fakeCarrier]byte
	copy(c[:32], message[fakeRandomAt:fakeRandomAt+32])
	copy(c[32:], message[fakeSessionAt:fakeSessionAt+32])
	return c
}

// fakeScrubbed is the associated data of the carrier: the label, then the
// hello with its carrier bytes zeroed.
func fakeScrubbed(message []byte) []byte {
	aad := make([]byte, 0, len(fakeAAD)+len(message))
	aad = append(aad, fakeAAD...)
	aad = append(aad, message...)
	at := len(fakeAAD)
	for i := 0; i < 32; i++ {
		aad[at+fakeRandomAt+i] = 0
		aad[at+fakeSessionAt+i] = 0
	}
	return aad
}

// The wui-faketls labels, in W-UI's own scheme.
var (
	fakeSalt       = []byte(label + "faketls")
	fakeSealInfo   = label + "faketls seal"
	fakeBinderInfo = label + "faketls binder"
	fakeNodeInfo   = label + "faketls node"
	fakeAAD        = []byte(label + "faketls aad")
)

// fakePRK is prk_f = HKDF-Extract(salt, ikm = dh).
func fakePRK(dh []byte) []byte {
	prk, err := hkdf.Extract(sha256.New, dh, fakeSalt)
	if err != nil {
		panic(err)
	}
	return prk
}

func fakeExpand(prk []byte, info string, parts ...[]byte) []byte {
	b := []byte(info)
	for _, p := range parts {
		b = append(b, p...)
	}
	out, err := hkdf.Expand(sha256.New, prk, string(b), 32)
	if err != nil {
		panic(err)
	}
	return out
}

// fakeSealKey is k_seal, new for every share X.
func fakeSealKey(prk []byte, share, node, inbound []byte) []byte {
	return fakeExpand(prk, fakeSealInfo, share, node, inbound)
}

// fakeBinderB is b, the key the proof's binder is computed from.
func fakeBinderB(prk, share []byte) []byte {
	return fakeExpand(prk, fakeBinderInfo, share)
}

// fakeNodeSeed is the 40-byte seed of the throwaway leaf.
func fakeNodeSeed(prk, share []byte, carrier [fakeCarrier]byte) [40]byte {
	b := []byte(fakeNodeInfo)
	b = append(b, share...)
	b = append(b, carrier[:]...)
	out, err := hkdf.Expand(sha256.New, prk, string(b), 40)
	if err != nil {
		panic(err)
	}
	var seed [40]byte
	copy(seed[:], out)
	return seed
}

// fakeBinder is the 16-byte binder of a proof: HMAC(k_bind, b | plain[0:28]).
func fakeBinder(bind, b, head []byte) [TagLen]byte {
	h := hmac.New(sha256.New, bind)
	h.Write(b)
	h.Write(head)
	var out [TagLen]byte
	copy(out[:], h.Sum(nil))
	return out
}

// x25519 is X25519(scalar, point); it returns all-zero for a point of low
// order, which no sealer can have made a key of.
func x25519(scalar, point []byte) []byte {
	priv, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		return make([]byte, 32)
	}
	pub, err := ecdh.X25519().NewPublicKey(point)
	if err != nil {
		return make([]byte, 32)
	}
	dh, err := priv.ECDH(pub)
	if err != nil {
		return make([]byte, 32) // a low-order point
	}
	return dh
}

// x25519Base is X25519(scalar, base point): the public share of a scalar.
func x25519Base(scalar []byte) []byte {
	priv, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		return make([]byte, 32)
	}
	return priv.PublicKey().Bytes()
}

// LeafKey is the gateway's throwaway leaf key for a node seed: d = (c mod
// (n-1)) + 1, with c the seed as a big-endian integer and n the P-256 group
// order. It returns the ECDSA key the gateway signs its handshake with and
// the public point in the uncompressed SEC1 form the client checks. A seed
// drawn at random gives a key that matches nothing a client computes.
func LeafKey(seed [40]byte) (*ecdsa.PrivateKey, []byte) {
	n := elliptic.P256().Params().N
	c := new(big.Int).SetBytes(seed[:])
	mod := new(big.Int).Sub(n, big.NewInt(1))
	d := new(big.Int).Mod(c, mod)
	d.Add(d, big.NewInt(1))
	x, y := elliptic.P256().ScalarBaseMult(d.FillBytes(make([]byte, 32)))
	priv := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: d}
	point, err := ecdh.P256().NewPrivateKey(d.FillBytes(make([]byte, 32)))
	if err != nil {
		return nil, nil
	}
	return priv, point.PublicKey().Bytes()
}

// FakeOutcome is what the gateway makes of a complete ClientHello.
type FakeKind int

const (
	// FakeStranger is a hello that is no proof: relay it to the site.
	FakeStranger FakeKind = iota
	// FakeRefused is a carrier that opened but failed a later check: answer
	// with a handshake whose leaf is random, open no session.
	FakeRefused
	// FakeProved is a hello that passed every check of the proof.
	FakeProved
)

// FakeProof is a proof that passed, before the replay rule and the limits.
type FakeProof struct {
	Kind      FakeKind
	Device    any // what the finder returned for the matched hour tag
	Timestamp uint64
	Share     [32]byte
	Seed      [40]byte
}

// FakeNode holds what the gateway needs to open a proof: the transport's
// private key, its public key, the inbound's WireGuard public key, and a
// dummy binder key a tag the table does not hold is checked with, so an
// unknown tag costs the same work as a known one.
type FakeNode struct {
	secret   []byte
	public   []byte
	inbound  []byte
	dummyBnd []byte
}

// NewFakeNode makes a node from the transport's private key and the inbound's
// public key. dummy is 32 random bytes nobody else holds.
func NewFakeNode(secret, inbound, dummy []byte) *FakeNode {
	return &FakeNode{
		secret:  append([]byte(nil), secret...),
		public:  x25519Base(secret),
		inbound: append([]byte(nil), inbound...),
		// A device the table does not hold is checked with a key nobody
		// else holds, so an unknown tag costs the same HMAC as a known one.
		dummyBnd: append([]byte(nil), dummy...),
	}
}

// Open does the constant-work verification of a complete ClientHello message:
// the same X25519, the same AEAD open, and the same lookup and binder whether
// the hello is a proof or not. names are the site names a hello may ask for.
// find returns a device's bind key (and an opaque handle) for an hour tag, or
// ok=false.
func (nd *FakeNode) Open(message []byte, names [][]byte, find func(hourTag [TagLen]byte) (device any, bind []byte, ok bool)) FakeProof {
	hello, parsedOK := ParseHello(message)
	named := parsedOK && hello.TLS13 && nameIn(names, hello.Name)

	var share [32]byte
	if parsedOK {
		copy(share[:], message[hello.Share:hello.Share+32])
	} else {
		share = fakeDummyShare
	}
	dh := x25519(nd.secret, share[:])

	var carrier [fakeCarrier]byte
	var aad []byte
	if parsedOK {
		carrier = fakeCarrierBytes(message)
		aad = fakeScrubbed(message)
	} else {
		aad = append([]byte(nil), fakeAAD...)
	}
	prk := fakePRK(dh)
	kSeal := fakeSealKey(prk, share[:], nd.public, nd.inbound)
	var plain [fakePlain]byte
	opened := false
	if a, err := chacha20poly1305.New(kSeal); err == nil {
		if out, err := a.Open(nil, make([]byte, 12), carrier[:], aad); err == nil && len(out) == fakePlain {
			copy(plain[:], out)
			opened = true
		}
	}

	version := plain[0] ^ fakeVersion
	zeros := version
	for _, c := range plain[1:4] {
		zeros |= c
	}
	for _, c := range plain[44:fakePlain] {
		zeros |= c
	}
	timestamp := binary.BigEndian.Uint64(plain[4:12])
	var hourTag [TagLen]byte
	copy(hourTag[:], plain[12:28])

	device, bind, found := find(hourTag)
	if !found {
		bind = nd.dummyBnd
	}
	b := fakeBinderB(prk, share[:])
	mac := fakeBinder(bind, b, plain[0:28])
	bound := subtle.ConstantTimeCompare(mac[:], plain[28:44]) == 1

	if !(named && opened && !allZero(dh)) {
		return FakeProof{Kind: FakeStranger}
	}
	if found && zeros == 0 && bound {
		return FakeProof{Kind: FakeProved, Device: device, Timestamp: timestamp, Share: share, Seed: fakeNodeSeed(prk, share[:], carrier)}
	}
	return FakeProof{Kind: FakeRefused}
}

func nameIn(names [][]byte, name []byte) bool {
	for _, n := range names {
		if subtle.ConstantTimeCompare(n, name) == 1 {
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	var v byte
	for _, c := range b {
		v |= c
	}
	return v == 0
}

// FakeHello builds a TLS 1.3 ClientHello the gateway reads: SNI name, a
// standalone X25519 key_share of x's public value, the three TLS 1.3 suites,
// and the extensions a real hello carries. The carrier bytes -- the random and
// the legacy session id -- are left zero for Carry to seal. x is a 32-byte
// X25519 scalar; a bad one is ok=false.
func FakeHello(name string, x []byte) ([]byte, bool) {
	priv, err := ecdh.X25519().NewPrivateKey(x)
	if err != nil {
		return nil, false
	}
	share := priv.PublicKey().Bytes()
	ext := func(kind uint16, body []byte) []byte {
		b := []byte{byte(kind >> 8), byte(kind), byte(len(body) >> 8), byte(len(body))}
		return append(b, body...)
	}
	// server_name.
	nameList := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...)
	sn := append([]byte{byte(len(nameList) >> 8), byte(len(nameList))}, nameList...)
	// supported_versions: TLS 1.3 alone.
	sv := []byte{2, 3, 4}
	// supported_groups: x25519.
	sg := []byte{0, 2, 0, 0x1d}
	// signature_algorithms: a handful a real hello offers.
	sa := []byte{0, 8, 0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x02, 0x01}
	// key_share: one standalone x25519 value.
	ks := append([]byte{0x00, 0x1d, 0x00, 0x20}, share...)
	ksBody := append([]byte{byte(len(ks) >> 8), byte(len(ks))}, ks...)

	exts := make([]byte, 0, 128)
	exts = append(exts, ext(0, sn)...)
	exts = append(exts, ext(10, sg)...)
	exts = append(exts, ext(13, sa)...)
	exts = append(exts, ext(43, sv)...)
	exts = append(exts, ext(51, ksBody)...)

	body := make([]byte, 0, 128)
	body = append(body, 3, 3)                // legacy_version
	body = append(body, make([]byte, 32)...) // random, sealed later
	body = append(body, 32)                  // session id length
	body = append(body, make([]byte, 32)...) // session id, sealed later
	body = append(body, 0, 6, 0x13, 0x01, 0x13, 0x02, 0x13, 0x03)
	body = append(body, 1, 0) // compression: null
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	msg := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...), true
}

// FakeHelloHybrid builds a ClientHello whose single key_share is the
// X25519MLKEM768 hybrid group: the value is the ML-KEM-768 encapsulation key
// ek (1184 bytes) followed by the X25519 public value of x. The proof is still
// keyed to the X25519 part. ek is the client's own encapsulation key, kept so
// the server's ciphertext can be decapsulated. The carrier is left zero for
// Carry to seal.
func FakeHelloHybrid(name string, x, ek []byte) ([]byte, bool) {
	priv, err := ecdh.X25519().NewPrivateKey(x)
	if err != nil || len(ek) != 1184 {
		return nil, false
	}
	share := priv.PublicKey().Bytes()
	ext := func(kind uint16, body []byte) []byte {
		b := []byte{byte(kind >> 8), byte(kind), byte(len(body) >> 8), byte(len(body))}
		return append(b, body...)
	}
	nameList := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...)
	sn := append([]byte{byte(len(nameList) >> 8), byte(len(nameList))}, nameList...)
	sv := []byte{2, 3, 4}
	sg := []byte{0, 2, 0x11, 0xec} // supported_groups: X25519MLKEM768
	sa := []byte{0, 8, 0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x02, 0x01}
	value := append(append([]byte(nil), ek...), share...)
	ks := append([]byte{0x11, 0xec, byte(len(value) >> 8), byte(len(value))}, value...)
	ksBody := append([]byte{byte(len(ks) >> 8), byte(len(ks))}, ks...)

	exts := make([]byte, 0, 1400)
	exts = append(exts, ext(0, sn)...)
	exts = append(exts, ext(10, sg)...)
	exts = append(exts, ext(13, sa)...)
	exts = append(exts, ext(43, sv)...)
	exts = append(exts, ext(51, ksBody)...)

	body := make([]byte, 0, 1500)
	body = append(body, 3, 3)
	body = append(body, make([]byte, 32)...)
	body = append(body, 32)
	body = append(body, make([]byte, 32)...)
	body = append(body, 0, 6, 0x13, 0x01, 0x13, 0x02, 0x13, 0x03)
	body = append(body, 1, 0)
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	msg := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...), true
}

// FakeClient is a device's side: it seals the proof into a ClientHello it has
// built, and tells the client which leaf key to expect.
type FakeClient struct {
	node    []byte
	inbound []byte
	keys    DeviceKeys
}

// NewFakeClient makes a client from the node's transport public key, the
// device's keys, and the inbound's public key.
func NewFakeClient(node []byte, keys DeviceKeys, inbound []byte) *FakeClient {
	return &FakeClient{node: append([]byte(nil), node...), inbound: append([]byte(nil), inbound...), keys: keys}
}

// Carry seals the carrier of message -- a ClientHello whose standalone X25519
// share is X25519(x) -- into its random and legacy session id. timestamp is
// the client's clock in Unix seconds.
func (c *FakeClient) Carry(message []byte, x []byte, timestamp int64) bool {
	hello, ok := ParseHello(message)
	if !ok {
		return false
	}
	share := x25519Base(x)
	if subtle.ConstantTimeCompare(message[hello.Share:hello.Share+32], share) != 1 {
		return false
	}
	dh := x25519(x, c.node)
	if allZero(dh) {
		return false
	}
	prk := fakePRK(dh)
	var plain [fakePlain]byte
	plain[0] = fakeVersion
	binary.BigEndian.PutUint64(plain[4:12], uint64(timestamp))
	hourTag := c.keys.HourTag(FakeHourOf(timestamp))
	copy(plain[12:28], hourTag[:])
	b := fakeBinderB(prk, share)
	mac := fakeBinder(c.keys.Bind, b, plain[0:28])
	copy(plain[28:44], mac[:])

	kSeal := fakeSealKey(prk, share, c.node, c.inbound)
	a, err := chacha20poly1305.New(kSeal)
	if err != nil {
		return false
	}
	sealed := a.Seal(nil, make([]byte, 12), plain[:], fakeScrubbed(message))
	if len(sealed) != fakeCarrier {
		return false
	}
	copy(message[fakeRandomAt:fakeRandomAt+32], sealed[:32])
	copy(message[fakeSessionAt:fakeSessionAt+32], sealed[32:])
	return true
}

// Leaf is the uncompressed public point of the gateway's throwaway leaf for a
// hello Carry sealed with x. The client checks the leaf's certificate key
// against it and against nothing else.
func (c *FakeClient) Leaf(x []byte, message []byte) []byte {
	if _, ok := ParseHello(message); !ok {
		return nil
	}
	dh := x25519(x, c.node)
	prk := fakePRK(dh)
	seed := fakeNodeSeed(prk, x25519Base(x), fakeCarrierBytes(message))
	_, point := LeafKey(seed)
	return point
}
