package stealth

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// The UDP packets.
//
// veil has two packets, and neither has a byte the network can name. An
// opening starts a session; data packets follow it, both ways.
//
//	opening = masked tag (16) | salt (32) | Seal(k_open, nonce 0, aad = the 48 bytes before,
//	                                             version | 0 0 0 | timestamp (8) | chunk)
//	data    = nonce (12, random) | Seal(k_up or k_down, nonce, no aad, chunk)
//
// The masked tag is the device's tag for the timestamp's epoch, XORed with the
// HMAC of the salt under the tunnel's mask key, so it differs in every packet
// even though the server finds the device by it with one lookup. The salt is
// fresh for every opening, and the opening's key and the session's two keys
// all come from it and the device's UDP root:
//
//	prk    = HKDF-Extract(salt, UDP root)
//	k_open = HKDF-Expand(prk, "open"), k_up = "up", k_down = "down"
//
// so k_open seals one message only, and its fixed nonce is used once. An
// opening's chunk holds exactly one WireGuard datagram -- a handshake
// initiation, or a keepalive of a tunnel that is up, never user data -- and a
// data packet's chunk holds one or none; a padding-only data packet is how a
// session answers without saying anything.
//
// A data packet carries no number, so a recorded one opens again. That is
// harmless: inside is a WireGuard datagram, and WireGuard refuses a repeat.
// What a replayed opening could do -- open a session from another address --
// the server's replay rule stops, by the salt.

// Sizes of the UDP packets.
const (
	udpNonce  = chacha20poly1305.NonceSize
	saltLen   = 32
	headerLen = 12
	// DataMin is the shortest data packet: a nonce and the seal of an empty
	// chunk.
	DataMin = udpNonce + Overhead
	// OpeningMin is the shortest opening: the masked tag, the salt, the header
	// and the seal.
	OpeningMin = TagLen + saltLen + headerLen + Overhead
	// FirstAnswer is a WireGuard handshake response, 92 bytes, in an unpadded
	// data packet. No opening is smaller: the server's first answer is never
	// larger than the opening it answers, so that nobody can make the server
	// send more than it was sent, and the response has to fit in it.
	FirstAnswer = DataMin + 2 + 92
	// UDPOverhead is what a full-size inner IP packet grows by on its way into
	// a data packet: WireGuard's 32 bytes, then the nonce, the length and the
	// seal. No veil packet is larger than the tunnel's MTU plus this.
	UDPOverhead = 32 + udpNonce + 2 + Overhead
	// ClientRenew is when a client opens a new session: this many packets
	// sent or received on the one it has.
	ClientRenew = 1 << 30
	// KeyPackets is the most any one key seals. Random 96-bit nonces stay
	// safely apart up to here; the server ends a session that reaches it.
	KeyPackets = 1 << 31
)

// Client timers.
var (
	// Retries is when a client sends an opening that had no answer again;
	// after the last, the attempt is lost.
	Retries = [...]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
)

// Errors of the UDP packets. A server never tells a peer which one it met: it
// drops the datagram whatever went wrong.
var (
	ErrRefused = errors.New("stealth: not a packet for this server")
	ErrSize    = errors.New("stealth: packet does not fit")
	ErrNumber  = errors.New("stealth: key has sealed all it may")
)

// UDPKeys are a session's two keys.
type UDPKeys struct {
	Up   []byte // client to server
	Down []byte // server to client
}

// salted is a salt's HKDF pseudorandom key under a device's UDP root.
type salted []byte

func (k DeviceKeys) salted(salt []byte) salted {
	prk, err := hkdf.Extract(sha256.New, k.UDP, salt)
	if err != nil {
		panic(err)
	}
	return prk
}

func (s salted) key(what string) []byte {
	k, err := hkdf.Expand(sha256.New, s, label+"udp "+what, chacha20poly1305.KeySize)
	if err != nil {
		panic(err)
	}
	return k
}

func (s salted) session() UDPKeys { return UDPKeys{Up: s.key("up"), Down: s.key("down")} }

func newAEAD(key []byte) cipher.AEAD {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		panic(err) // every key here is derived at the right length
	}
	return a
}

// UDPClient is what a device holds to open sessions on one tunnel.
type UDPClient struct {
	keys    DeviceKeys
	maskKey []byte
}

// NewUDPClient is a device's client for the tunnel anchor names.
func NewUDPClient(secret Secret, anchor Anchor) *UDPClient {
	return &UDPClient{keys: Derive(secret, anchor), maskKey: MaskKey(anchor)}
}

// Open appends an opening around datagram to dst and gives the keys of the
// session it starts. now is the client's clock, which the timestamp and the
// epoch both come from; pad is the padder of the direction the session sends
// in; room is the largest packet the path takes, the tunnel's MTU plus
// UDPOverhead. On an error dst comes back as it was.
func (c *UDPClient) Open(dst []byte, now time.Time, datagram []byte, pad *Padder, room int) ([]byte, UDPKeys, error) {
	if len(datagram) < MinDatagram || len(datagram) > MaxDatagram {
		return dst, UDPKeys{}, fmt.Errorf("stealth: datagram of %d bytes", len(datagram))
	}
	used, limit := 2+len(datagram), min(room-OpeningMin, MaxPlain)
	if used > limit {
		return dst, UDPKeys{}, ErrSize
	}
	// The server's first answer is no larger than this packet, so a
	// keepalive and a small draw are padded up to where a handshake response
	// still fits in that answer.
	padding := max(pad.Wide(), FirstAnswer-OpeningMin-used)
	padding = min(padding, limit-used)

	var salt [saltLen]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return dst, UDPKeys{}, fmt.Errorf("stealth: read entropy: %w", err)
	}
	ts := uint64(now.Unix())
	tag := c.keys.EpochTag(Epoch(now))
	m := mask(c.maskKey, salt[:])

	start := len(dst)
	for i := range tag {
		dst = append(dst, tag[i]^m[i])
	}
	dst = append(dst, salt[:]...)
	head := len(dst)
	dst = append(dst, Version, 0, 0, 0)
	dst = binary.BigEndian.AppendUint64(dst, ts)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(datagram)))
	dst = append(dst, datagram...)
	dst = append(dst, make([]byte, padding)...) // clipped above, against the chunk alone

	s := c.keys.salted(salt[:])
	aead := newAEAD(s.key("open"))
	dst = slices.Grow(dst, Overhead) // so the seal lands in dst, not a copy
	aad := dst[start:head]
	// Sealed in place: the plaintext starts at head and the tag is appended.
	sealed := aead.Seal(dst[head:head], make([]byte, udpNonce), dst[head:], aad)
	return dst[:head+len(sealed)], s.session(), nil
}

// UDPServer is what a server holds to read openings on one tunnel.
type UDPServer struct{ maskKey []byte }

// NewUDPServer is the server side of the tunnel anchor names.
func NewUDPServer(anchor Anchor) UDPServer { return UDPServer{maskKey: MaskKey(anchor)} }

// Tag is the tag a datagram names if it is an opening: one HMAC, and nothing
// is derived or opened. A datagram of a size no opening has names nothing.
// What a stranger sends costs the server this and one lookup.
func (s UDPServer) Tag(packet []byte) (Tag, bool) {
	if len(packet) < OpeningMin || len(packet) > OpeningMin+MaxPlain {
		return Tag{}, false
	}
	m := mask(s.maskKey, packet[TagLen:TagLen+saltLen])
	var t Tag
	subtle.XORBytes(t[:], packet[:TagLen], m[:])
	return t, true
}

// Opening is an opening a device sealed. It has authenticated the device's
// transport secret, and nothing more: what it carries is WireGuard's to judge,
// and whether it may start a session is the replay rule's.
type Opening struct {
	// Timestamp is the client's clock, Unix seconds.
	Timestamp uint64
	// Salt is the packet's key in the replay cache.
	Salt [saltLen]byte
	// Datagram is the one WireGuard datagram the opening carries. It aliases
	// the buffer Unseal was given.
	Datagram []byte
	prk      salted
}

// Keys derives the session's keys. It is only worth its cost for an opening
// the replay rule accepted.
func (o *Opening) Keys() UDPKeys { return o.prk.session() }

// Unseal opens a datagram as an opening of the device whose keys its tag
// found, into scratch. The version, the zero bytes and the chunk's framing are
// checked, and the chunk must hold exactly one datagram.
func (s UDPServer) Unseal(keys DeviceKeys, packet, scratch []byte) (Opening, error) {
	if len(packet) < OpeningMin || len(packet) > OpeningMin+MaxPlain {
		return Opening{}, ErrRefused
	}
	var o Opening
	copy(o.Salt[:], packet[TagLen:TagLen+saltLen])
	o.prk = keys.salted(o.Salt[:])
	head := TagLen + saltLen
	plain, err := newAEAD(o.prk.key("open")).Open(scratch[:0], make([]byte, udpNonce), packet[head:], packet[:head])
	if err != nil {
		return Opening{}, ErrRefused
	}
	if plain[0] != Version || plain[1] != 0 || plain[2] != 0 || plain[3] != 0 {
		return Opening{}, ErrRefused
	}
	o.Timestamp = binary.BigEndian.Uint64(plain[4:headerLen])
	d, err := onlyItem(plain[headerLen:])
	if err != nil || d == nil {
		return Opening{}, ErrRefused
	}
	o.Datagram = d
	return o, nil
}

// onlyItem is the one datagram of a chunk that holds one or none, or nil for
// none. A chunk of two is refused: that is a stream's chunk, not a packet's.
func onlyItem(plain []byte) ([]byte, error) {
	var d []byte
	n := 0
	if err := Items(plain, func(item []byte) { d = item; n++ }); err != nil || n > 1 {
		return nil, ErrRefused
	}
	return d, nil
}

// UDPSender seals one direction of a session. It is not safe for concurrent
// use.
type UDPSender struct {
	aead cipher.AEAD
	pad  Padder
	sent uint64
}

// NewUDPSender seals under key, padded by pad.
func NewUDPSender(key []byte, pad Padder) *UDPSender {
	return &UDPSender{aead: newAEAD(key), pad: pad}
}

// Sent is how many packets this key has sealed.
func (s *UDPSender) Sent() uint64 { return s.sent }

// Seal appends one data packet to dst: datagram, or padding alone when it is
// nil. wide pads it wide whatever the count says -- for a WireGuard handshake
// message that is so already. room is the largest packet that may leave, and
// padding is lowered to fit it, so a full-size inner packet carries none. On
// an error dst comes back as it was and nothing is counted; ErrNumber is
// final, and the session must end.
func (s *UDPSender) Seal(dst, datagram []byte, wide bool, room int) ([]byte, error) {
	if s.sent >= KeyPackets {
		return dst, ErrNumber
	}
	if datagram != nil && (len(datagram) < MinDatagram || len(datagram) > MaxDatagram) {
		return dst, fmt.Errorf("stealth: datagram of %d bytes", len(datagram))
	}
	used, limit := 0, min(room-DataMin, MaxPlain)
	if datagram != nil {
		used = 2 + len(datagram)
	}
	if limit < 0 || used > limit {
		return dst, ErrSize
	}
	padding := min(s.pad.Next(wide || IsHandshake(datagram)), limit-used)

	start := len(dst)
	dst = append(dst, make([]byte, udpNonce)...)
	if _, err := rand.Read(dst[start:]); err != nil {
		return dst[:start], fmt.Errorf("stealth: read entropy: %w", err)
	}
	body := len(dst)
	if datagram != nil {
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(datagram)))
		dst = append(dst, datagram...)
	}
	dst = append(dst, make([]byte, padding)...) // clipped above, against the chunk alone
	dst = slices.Grow(dst, Overhead)
	sealed := s.aead.Seal(dst[body:body], dst[start:body], dst[body:], nil)
	s.sent++
	return dst[:body+len(sealed)], nil
}

// UDPReceiver opens one direction of a session.
type UDPReceiver struct {
	aead     cipher.AEAD
	received uint64
}

// NewUDPReceiver opens under key.
func NewUDPReceiver(key []byte) *UDPReceiver { return &UDPReceiver{aead: newAEAD(key)} }

// Received is how many packets have opened, repeats included.
func (r *UDPReceiver) Received() uint64 { return r.received }

// Open opens a datagram as a data packet of this session, into scratch, and
// gives its WireGuard datagram, or nil for a padding-only packet. packet is
// left as it came, so a server can try it on the next session of the same
// source.
func (r *UDPReceiver) Open(packet, scratch []byte) ([]byte, error) {
	if len(packet) < DataMin || len(packet) > DataMin+MaxPlain {
		return nil, ErrRefused
	}
	plain, err := r.aead.Open(scratch[:0], packet[:udpNonce], packet[udpNonce:], nil)
	if err != nil {
		return nil, ErrRefused
	}
	d, err := onlyItem(plain)
	if err != nil {
		return nil, err
	}
	r.received++
	return d, nil
}
