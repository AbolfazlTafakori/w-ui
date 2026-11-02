package stealth

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// The stream proof.
//
// A stream transport opens with one HTTP request, and the request is the
// proof: its target is the transport's path and a token, and a server that
// cannot open the token treats the request as any other visitor's. The token
// is an HPKE message to the tunnel's transport key, base mode,
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 and ChaCha20-Poly1305:
//
//	token  = base64url(enc (32) | HPKE-Seal(transport key, info, no aad, hello))
//	info   = L | "proof" | 0 | transport name | 0 | anchor
//	hello  = version | 0 0 0 | timestamp (8) | tag (16) | binder (32) | zero padding (0..96)
//	binder = HMAC(k_bind, Export(L | "binder", 32) | hello[0..28])
//
// Only the holder of the transport key reads the hello, so the tag a device is
// found by never shows on the wire. The binder is what proves the secret: a
// tag alone is no proof, and the export ties the binder to this one HPKE
// context, so it cannot be lifted into another token. The transport's name
// and the anchor are in the info, so a token for one transport or one tunnel
// is no token for another. The path is not bound: a CDN may rewrite it.
//
// A token is taken once. The server's replay rule keeps enc, which is fresh in
// every token, the way veil keeps an opening's salt.
//
// The session's keys need both the proof's ephemeral key, through the
// export, and the device's secret:
//
//	prk    = HKDF-Extract(salt = Export(L | "stream", 32), ikm = k_stream)
//	k_up   = HKDF-Expand(prk, L | "stream up", 32)     client to server
//	k_down = HKDF-Expand(prk, L | "stream down", 32)   server to client
//	id     = HKDF-Expand(prk, L | "stream id", 16)     relay's session id

// Sizes of a proof.
const (
	encLen       = 32
	helloHead    = 4 + 8 + TagLen // version, three zeros, timestamp, tag
	binderLen    = 32
	helloMin     = helloHead + binderLen
	proofPadMax  = 96
	hpkeOverhead = chacha20poly1305.Overhead
	// TokenMin and TokenMax bound a token's raw bytes.
	TokenMin = encLen + helloMin + hpkeOverhead
	TokenMax = TokenMin + proofPadMax
	// TokenTextMin and TokenTextMax bound it as base64url without padding,
	// as a request carries it.
	TokenTextMin = (TokenMin*8 + 5) / 6
	TokenTextMax = (TokenMax*8 + 5) / 6
)

// TransportKeyLen is the length of a transport key, private or public.
const TransportKeyLen = 32

// ErrProof is any token that is not a proof for this server. A server answers
// it as it answers every request it does not know, so it says nothing more.
var ErrProof = errors.New("stealth: not a proof for this server")

func suite() (hpke.KEM, hpke.KDF, hpke.AEAD) {
	return hpke.DHKEM(ecdh.X25519()), hpke.HKDFSHA256(), hpke.ChaCha20Poly1305()
}

func proofInfo(transport string, anchor Anchor) []byte {
	info := make([]byte, 0, len(label)+6+len(transport)+1+len(anchor))
	info = append(info, label+"proof\x00"...)
	info = append(info, transport...)
	info = append(info, 0)
	return append(info, anchor[:]...)
}

// TransportKey is a tunnel's transport key pair: the private half stays with
// the server, the public half goes into every profile.
type TransportKey struct {
	priv hpke.PrivateKey
	pub  [TransportKeyLen]byte
}

// NewTransportKey draws a transport key.
func NewTransportKey() (TransportKey, error) {
	kem, _, _ := suite()
	k, err := kem.GenerateKey()
	if err != nil {
		return TransportKey{}, fmt.Errorf("stealth: draw a transport key: %w", err)
	}
	return transportKey(k)
}

// ParseTransportKey reads a private transport key written by String.
func ParseTransportKey(v string) (TransportKey, error) {
	var b [TransportKeyLen]byte
	if err := parse32(v, b[:]); err != nil {
		return TransportKey{}, fmt.Errorf("stealth: transport key: %w", err)
	}
	kem, _, _ := suite()
	k, err := kem.NewPrivateKey(b[:])
	if err != nil {
		return TransportKey{}, fmt.Errorf("stealth: transport key: %w", err)
	}
	return transportKey(k)
}

func transportKey(k hpke.PrivateKey) (TransportKey, error) {
	t := TransportKey{priv: k}
	copy(t.pub[:], k.PublicKey().Bytes())
	return t, nil
}

// String is the private key in standard base64, as the database keeps it.
func (k TransportKey) String() string {
	b, err := k.priv.Bytes()
	if err != nil {
		panic(err) // an X25519 key always serializes
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Public is the public key in standard base64, as a profile carries it.
func (k TransportKey) Public() string { return base64.StdEncoding.EncodeToString(k.pub[:]) }

// StreamKeys are a stream session's two keys, and the name relay's later
// requests give it.
type StreamKeys struct {
	Up   []byte // client to server
	Down []byte // server to client
	// ID is the session's id, as relay's later requests name it. Only the
	// proof's two ends can work it out, and it is no key: knowing it opens
	// nothing (see PollBody).
	ID [PollIDLen]byte
}

func streamKeys(exported, stream []byte) StreamKeys {
	prk, err := hkdf.Extract(sha256.New, stream, exported)
	if err != nil {
		panic(err)
	}
	key := func(what string) []byte {
		k, err := hkdf.Expand(sha256.New, prk, label+"stream "+what, chacha20poly1305.KeySize)
		if err != nil {
			panic(err)
		}
		return k
	}
	k := StreamKeys{Up: key("up"), Down: key("down")}
	id, err := hkdf.Expand(sha256.New, prk, label+"stream id", PollIDLen)
	if err != nil {
		panic(err)
	}
	copy(k.ID[:], id)
	return k
}

func export(ctx interface {
	Export(string, int) ([]byte, error)
}, what string) []byte {
	b, err := ctx.Export(label+what, 32)
	if err != nil {
		panic(err) // 32 bytes is always within what HPKE exports
	}
	return b
}

func binder(bind, exported, head []byte) []byte {
	h := hmac.New(sha256.New, bind)
	h.Write(exported)
	h.Write(head)
	return h.Sum(nil)
}

// ProofClient is what a device holds to open streams on one transport of one
// tunnel.
type ProofClient struct {
	keys   DeviceKeys
	server hpke.PublicKey
	info   []byte
}

// NewProofClient is a device's client for transport on the tunnel anchor
// names, whose transport key is server, in standard base64.
func NewProofClient(secret Secret, anchor Anchor, transport, server string) (*ProofClient, error) {
	var b [TransportKeyLen]byte
	if err := parse32(server, b[:]); err != nil {
		return nil, fmt.Errorf("stealth: the server's transport key: %w", err)
	}
	kem, _, _ := suite()
	pub, err := kem.NewPublicKey(b[:])
	if err != nil {
		return nil, fmt.Errorf("stealth: the server's transport key: %w", err)
	}
	return &ProofClient{keys: Derive(secret, anchor), server: pub, info: proofInfo(transport, anchor)}, nil
}

// Token is a fresh proof, as a request carries it, and the keys of the
// session it opens. now is the client's clock; padding is drawn from 0..96.
func (c *ProofClient) Token(now time.Time) (string, StreamKeys, error) {
	_, kdf, aead := suite()
	enc, sender, err := hpke.NewSender(c.server, kdf, aead, c.info)
	if err != nil {
		return "", StreamKeys{}, fmt.Errorf("stealth: seal a proof: %w", err)
	}
	hello := make([]byte, 0, helloMin+proofPadMax)
	hello = append(hello, Version, 0, 0, 0)
	hello = binary.BigEndian.AppendUint64(hello, uint64(now.Unix()))
	hello = append(hello, c.keys.Tag[:]...)
	hello = append(hello, binder(c.keys.Bind, export(sender, "binder"), hello)...)
	hello = append(hello, make([]byte, uniform(0, proofPadMax))...)
	ct, err := sender.Seal(nil, hello)
	if err != nil {
		return "", StreamKeys{}, fmt.Errorf("stealth: seal a proof: %w", err)
	}
	raw := append(enc, ct...)
	return base64.RawURLEncoding.EncodeToString(raw), streamKeys(export(sender, "stream"), c.keys.Stream), nil
}

// ProofServer is what a server holds to read the proofs of one transport of
// one tunnel. It holds the current transport key and, while devices still
// have profiles that name it, the one before.
type ProofServer struct {
	keys []hpke.PrivateKey
	info []byte
	// dummy stands in for the keys of a tag no device has, so the work a
	// proof costs does not say whether its tag is known.
	dummy DeviceKeys
}

// NewProofServer is the server side of transport on the tunnel anchor names.
// keys are its transport keys, the current first.
func NewProofServer(anchor Anchor, transport string, keys ...TransportKey) (*ProofServer, error) {
	if len(keys) == 0 {
		return nil, errors.New("stealth: a proof server needs a transport key")
	}
	s := &ProofServer{info: proofInfo(transport, anchor)}
	for _, k := range keys {
		s.keys = append(s.keys, k.priv)
	}
	var dummy Secret
	if _, err := rand.Read(dummy[:]); err != nil {
		return nil, fmt.Errorf("stealth: read entropy: %w", err)
	}
	s.dummy = Derive(dummy, anchor)
	return s, nil
}

// Proof is a token the server opened: what it says, before anything about
// its device is checked.
type Proof struct {
	// Enc is the token's ephemeral key, which the replay rule keeps.
	Enc       [encLen]byte
	Timestamp uint64
	Tag       Tag
	binder    [binderLen]byte
	head      [helloHead]byte
	exported  [2][]byte // binder, stream
}

// Open reads a token. Every transport key is tried whether an earlier one
// opened it or not, so the time it takes does not say which one did. A token
// that opens under none, or does not parse, is ErrProof.
func (s *ProofServer) Open(token string) (Proof, error) {
	if len(token) < TokenTextMin || len(token) > TokenTextMax {
		return Proof{}, ErrProof
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < TokenMin || len(raw) > TokenMax {
		return Proof{}, ErrProof
	}
	_, kdf, aead := suite()
	var (
		hello []byte
		rcpt  *hpke.Recipient
	)
	for _, k := range s.keys {
		r, err := hpke.NewRecipient(raw[:encLen], k, kdf, aead, s.info)
		if err != nil {
			continue // enc is not a point anybody could have sealed to
		}
		h, err := r.Open(nil, raw[encLen:])
		if err == nil && hello == nil {
			hello, rcpt = h, r
		}
	}
	if hello == nil || len(hello) < helloMin || hello[0] != Version || hello[1]|hello[2]|hello[3] != 0 {
		return Proof{}, ErrProof
	}
	for _, b := range hello[helloMin:] {
		if b != 0 {
			return Proof{}, ErrProof
		}
	}
	var p Proof
	copy(p.Enc[:], raw[:encLen])
	p.Timestamp = binary.BigEndian.Uint64(hello[4:12])
	copy(p.Tag[:], hello[12:helloHead])
	copy(p.head[:], hello[:helloHead])
	copy(p.binder[:], hello[helloHead:helloMin])
	p.exported = [2][]byte{export(rcpt, "binder"), export(rcpt, "stream")}
	return p, nil
}

// Check proves a proof against the device its tag found, and gives the keys
// of the session it opens. keys is nil for a tag no device has: the binder is
// worked out against a stand-in all the same, and the proof refused, so that
// a known tag and an unknown one cost the same.
func (s *ProofServer) Check(p *Proof, keys *DeviceKeys) (StreamKeys, error) {
	k := keys
	if k == nil {
		k = &s.dummy
	}
	want := binder(k.Bind, p.exported[0], p.head[:])
	if !hmac.Equal(want, p.binder[:]) || keys == nil {
		return StreamKeys{}, ErrProof
	}
	return streamKeys(p.exported[1], keys.Stream), nil
}
