package stealthgw

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/binary"
	"hash"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
)

// Which device a handshake initiation is from.
//
// A session belongs to the device whose transport secret sealed its opening.
// Its WireGuard traffic must be that device's too, or one customer's secret
// would carry another's tunnel: so every initiation that reaches the server
// on a session is opened as far as the responder's first two steps and must
// name the session's device as its initiator. The second step decrypts the
// timestamp under a key only the initiator's private key gives, so an
// initiation that passes was made by the device, not just labelled with its
// public key.
//
// This is WireGuard's own handshake, as its responder runs it, and changes
// nothing about it: the kernel takes the same initiation afterwards and does
// the rest.

const (
	noiseConstruction = "Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s"
	noiseIdentifier   = "WireGuard v1 zx2c4 Jason@zx2c4.com"
	labelMAC1         = "mac1----"

	initiationLen = 148
)

// responder is the tunnel's side of the handshake: its key, and what follows
// from it alone, worked out once.
type responder struct {
	priv    *ecdh.PrivateKey
	hash    [32]byte // the handshake hash after the responder's public key
	chain   [32]byte // the chaining key before the first message
	mac1Key [32]byte
}

func newResponder(private [32]byte) (*responder, error) {
	priv, err := ecdh.X25519().NewPrivateKey(private[:])
	if err != nil {
		return nil, err
	}
	pub := priv.PublicKey().Bytes()
	r := &responder{priv: priv}
	r.chain = blake2s.Sum256([]byte(noiseConstruction))
	r.hash = mixHash(mixHash(r.chain, []byte(noiseIdentifier)), pub)
	r.mac1Key = blake2s.Sum256(append([]byte(labelMAC1), pub...))
	return r, nil
}

// static is the static-static secret with a device, which every initiation
// of that device needs and which never changes: computed once per device.
// ok is false for a public key that gives no secret.
func (r *responder) static(device [32]byte) (ss [32]byte, ok bool) {
	pub, err := ecdh.X25519().NewPublicKey(device[:])
	if err != nil {
		return ss, false
	}
	b, err := r.priv.ECDH(pub)
	if err != nil {
		return ss, false // a small-order key: crypto/ecdh refuses the zero secret
	}
	copy(ss[:], b)
	return ss, true
}

// isInitiation says whether a datagram is shaped as a handshake initiation.
func isInitiation(d []byte) bool {
	return len(d) == initiationLen && d[0] == 1 && d[1] == 0 && d[2] == 0 && d[3] == 0
}

// from says whether an initiation is device's, made with device's private
// key: its MAC1 is right for this tunnel, its encrypted static key is device,
// and its timestamp opens under the static-static secret ss.
func (r *responder) from(msg []byte, device, ss [32]byte) bool {
	if !isInitiation(msg) {
		return false
	}
	mac1, _ := blake2s.New128(r.mac1Key[:])
	mac1.Write(msg[:116])
	if subtle.ConstantTimeCompare(mac1.Sum(nil), msg[116:132]) != 1 {
		return false
	}
	eph := msg[8:40]
	encStatic := msg[40:88]
	encStamp := msg[88:116]

	chain := kdf1(r.chain, eph)
	h := mixHash(r.hash, eph)

	ephPub, err := ecdh.X25519().NewPublicKey(eph)
	if err != nil {
		return false
	}
	es, err := r.priv.ECDH(ephPub)
	if err != nil {
		return false
	}
	var key [32]byte
	chain, key = kdf2(chain, es)
	static, err := open(key, encStatic, h[:])
	if err != nil || subtle.ConstantTimeCompare(static, device[:]) != 1 {
		return false
	}
	h = mixHash(h, encStatic)

	_, key = kdf2(chain, ss[:])
	_, err = open(key, encStamp, h[:])
	return err == nil
}

func open(key [32]byte, sealed, aad []byte) ([]byte, error) {
	a, _ := chacha20poly1305.New(key[:])
	return a.Open(nil, make([]byte, chacha20poly1305.NonceSize), sealed, aad)
}

func mixHash(h [32]byte, data []byte) [32]byte {
	d, _ := blake2s.New256(nil)
	d.Write(h[:])
	d.Write(data)
	var out [32]byte
	d.Sum(out[:0])
	return out
}

func newBlake() hash.Hash {
	d, _ := blake2s.New256(nil)
	return d
}

func hmacBlake(key, in []byte) [32]byte {
	m := hmac.New(newBlake, key)
	m.Write(in)
	var out [32]byte
	m.Sum(out[:0])
	return out
}

func kdf1(chain [32]byte, in []byte) [32]byte {
	t0 := hmacBlake(chain[:], in)
	return hmacBlake(t0[:], []byte{1})
}

func kdf2(chain [32]byte, in []byte) (t1, t2 [32]byte) {
	t0 := hmacBlake(chain[:], in)
	t1 = hmacBlake(t0[:], []byte{1})
	t2 = hmacBlake(t0[:], append(t1[:], 2))
	return t1, t2
}

// Fields a datagram shows in the clear, which the gateway reads to tie
// WireGuard's sessions to its own.

// wgType is a datagram's message type.
func wgType(d []byte) byte {
	if len(d) < 4 {
		return 0
	}
	return d[0]
}

// isKeepalive says whether a datagram is a transport message with nothing in
// it: the header, the counter and the tag alone.
func isKeepalive(d []byte) bool { return len(d) == 32 && d[0] == 4 }

// receiverIndex is the index a transport message is addressed to, and
// transportCounter its counter.
func receiverIndex(d []byte) uint32    { return binary.LittleEndian.Uint32(d[4:8]) }
func transportCounter(d []byte) uint64 { return binary.LittleEndian.Uint64(d[8:16]) }

// responseSender is the index a handshake response names its sender by: the
// one the initiator addresses its transport messages to from then on.
func responseSender(d []byte) (uint32, bool) {
	if len(d) != 92 || d[0] != 2 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(d[4:8]), true
}
