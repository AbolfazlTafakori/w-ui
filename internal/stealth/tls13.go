package stealth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"hash"

	"golang.org/x/crypto/chacha20poly1305"
)

// The pieces of TLS 1.3 (RFC 8446) that mirror writes itself, because no
// TLS library lets it set the sizes of records or the bytes of a hello: the
// three suites, the key schedule of section 7.1, the record protection of
// section 5 with record padding (section 5.4), and the ServerHello of section
// 4.1.3 with the two key-exchange groups the mode answers with. Nothing here
// reads a socket or a clock.

// TLS record sizes.
const (
	// TLSHeader is the record header, and TLSTag the tag every protected
	// record ends with.
	TLSHeader = 5
	TLSTag    = 16
	// TLSRecord is the most a protected record holds: 2^14 inner plaintext,
	// the content type, and up to 256 of expansion.
	TLSRecord = 16384 + 256
	// tlsMaxInner is the most inner plaintext plus padding a record carries.
	tlsMaxInner = 16384
)

// The two key-exchange groups the mode answers with.
const (
	GroupX25519 uint16 = 0x001d
	GroupHybrid uint16 = 0x11ec
	// kemCiphertext is the ML-KEM-768 ciphertext, before the X25519 part.
	kemCiphertext = 1088
)

// helloRetry is the ServerHello random that marks a HelloRetryRequest.
var helloRetry = [32]byte{
	0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c,
}

// Suite is one of the three TLS 1.3 cipher suites of RFC 8446 section 9.1.
type Suite int

const (
	SuiteAES128 Suite = iota
	SuiteAES256
	SuiteChacha
)

// SuiteFromID reads a suite's id, or ok=false.
func SuiteFromID(id uint16) (Suite, bool) {
	switch id {
	case 0x1301:
		return SuiteAES128, true
	case 0x1302:
		return SuiteAES256, true
	case 0x1303:
		return SuiteChacha, true
	}
	return 0, false
}

// ID is the suite's wire id.
func (s Suite) ID() uint16 {
	switch s {
	case SuiteAES128:
		return 0x1301
	case SuiteAES256:
		return 0x1302
	default:
		return 0x1303
	}
}

// HashLen is the length of the suite's hash, which every secret of its key
// schedule has.
func (s Suite) HashLen() int {
	if s == SuiteAES256 {
		return 48
	}
	return 32
}

func (s Suite) newHash() hash.Hash {
	if s == SuiteAES256 {
		return sha512.New384()
	}
	return sha256.New()
}

func (s Suite) hashFn() func() hash.Hash {
	if s == SuiteAES256 {
		return sha512.New384
	}
	return sha256.New
}

func (s Suite) keyLen() int {
	if s == SuiteAES128 {
		return 16
	}
	return 32
}

func (s Suite) aead(key []byte) (cipher.AEAD, error) {
	if s == SuiteChacha {
		return chacha20poly1305.New(key)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// records is how many records may be sealed under one key: AES-GCM ends well
// before 2^24.5, and nothing here sends a KeyUpdate.
func (s Suite) records() uint64 {
	if s == SuiteChacha {
		return 1 << 60
	}
	return 1 << 24
}

// Transcript is the running hash of the handshake messages.
type Transcript struct{ h hash.Hash }

func NewTranscript(s Suite) *Transcript { return &Transcript{h: s.newHash()} }

func (t *Transcript) Update(message []byte) { t.h.Write(message) }

func (t *Transcript) Hash() []byte {
	// hash.Hash has no clone in std; keep it simple by summing a copy is not
	// possible, so callers take the hash at each point they need it and keep
	// feeding. Sum(nil) does not reset the state.
	return t.h.Sum(nil)
}

// expandLabel is HKDF-Expand-Label of RFC 8446 section 7.1 over a secret used
// as the pseudorandom key.
func expandLabel(s Suite, secret []byte, name string, context []byte, size int) []byte {
	full := "tls13 " + name
	info := make([]byte, 0, 2+1+len(full)+1+len(context))
	info = append(info, byte(size>>8), byte(size))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	out, err := hkdf.Expand(s.hashFn(), secret, string(info), size)
	if err != nil {
		panic(err)
	}
	return out
}

func (s Suite) emptyHash() []byte {
	h := s.newHash()
	return h.Sum(nil)
}

func extract(s Suite, salt, ikm []byte) []byte {
	prk, err := hkdf.Extract(s.hashFn(), ikm, salt)
	if err != nil {
		panic(err)
	}
	return prk
}

// Secrets are the two traffic secrets of one extract, and the salt of the
// next extract.
type Secrets struct {
	Client, Server, Next []byte
}

func deriveSecrets(s Suite, salt, input []byte, names [2]string, hashOf []byte) Secrets {
	prk := extract(s, salt, input)
	return Secrets{
		Client: expandLabel(s, prk, names[0], hashOf, s.HashLen()),
		Server: expandLabel(s, prk, names[1], hashOf, s.HashLen()),
		Next:   expandLabel(s, prk, "derived", s.emptyHash(), s.HashLen()),
	}
}

// HandshakeSecrets are the handshake traffic secrets from the key agreement
// shared and the transcript hash through the ServerHello.
func HandshakeSecrets(s Suite, shared, helloHash []byte) Secrets {
	zeros := make([]byte, s.HashLen())
	early := extract(s, zeros, zeros)
	salt := expandLabel(s, early, "derived", s.emptyHash(), s.HashLen())
	return deriveSecrets(s, salt, shared, [2]string{"c hs traffic", "s hs traffic"}, helloHash)
}

// ApplicationSecrets are the application traffic secrets of the client and the
// server, from the salt HandshakeSecrets gave and the transcript hash through
// the server's Finished.
func ApplicationSecrets(s Suite, salt, hashOf []byte) (client, server []byte) {
	sec := deriveSecrets(s, salt, make([]byte, s.HashLen()), [2]string{"c ap traffic", "s ap traffic"}, hashOf)
	return sec.Client, sec.Server
}

// Finished is the verify data of a Finished message for a side's handshake
// traffic secret and the transcript hash before it.
func Finished(s Suite, secret, hashOf []byte) []byte {
	key := expandLabel(s, secret, "finished", nil, s.HashLen())
	m := hmac.New(s.hashFn(), key)
	m.Write(hashOf)
	return m.Sum(nil)
}

// TLSKeys is one direction of the record layer: the key and iv of a traffic
// secret, and the record number, which starts at 0 with every key.
type TLSKeys struct {
	aead   cipher.AEAD
	iv     [12]byte
	number uint64
	limit  uint64
}

// NewTLSKeys makes a direction's keys from a traffic secret.
func NewTLSKeys(s Suite, secret []byte) (*TLSKeys, error) {
	key := expandLabel(s, secret, "key", nil, s.keyLen())
	iv := expandLabel(s, secret, "iv", nil, 12)
	a, err := s.aead(key)
	if err != nil {
		return nil, err
	}
	k := &TLSKeys{aead: a, limit: s.records()}
	copy(k.iv[:], iv)
	return k, nil
}

// Number is how many records have been sealed or opened under this key.
func (k *TLSKeys) Number() uint64 { return k.number }

func (k *TLSKeys) nonce() []byte {
	var nonce [12]byte
	nonce = k.iv
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], k.number)
	for i := 0; i < 8; i++ {
		nonce[4+i] ^= ctr[i]
	}
	return nonce[:]
}

// Seal appends one record of content type kind holding plaintext and pad zero
// bytes of record padding after it. False when the keys have sealed all they
// may or the record would not fit; then nothing is appended.
func (k *TLSKeys) Seal(kind byte, plaintext []byte, pad int, out []byte) ([]byte, bool) {
	if k.number >= k.limit || len(plaintext)+pad > tlsMaxInner {
		return out, false
	}
	length := len(plaintext) + 1 + pad + TLSTag
	start := len(out)
	out = append(out, 23, 3, 3, byte(length>>8), byte(length))
	header := out[start : start+TLSHeader]
	inner := make([]byte, 0, len(plaintext)+1+pad)
	inner = append(inner, plaintext...)
	inner = append(inner, kind)
	inner = append(inner, make([]byte, pad)...)
	out = k.aead.Seal(out, k.nonce(), inner, header)
	k.number++
	return out, true
}

// Open opens the body of a record whose header was header: the content type
// and the content, or ok=false for a record that does not open or holds no
// content type. body is used as scratch.
func (k *TLSKeys) Open(header []byte, body []byte) (kind byte, content []byte, ok bool) {
	if k.number >= k.limit {
		return 0, nil, false
	}
	plain, err := k.aead.Open(body[:0], k.nonce(), body, header)
	if err != nil {
		return 0, nil, false
	}
	k.number++
	end := len(plain) - 1
	for end >= 0 && plain[end] == 0 {
		end--
	}
	if end < 0 {
		return 0, nil, false
	}
	return plain[end], plain[:end], true
}

// tlsCursor reads a message from the front; a read past the end is ok=false.
type tlsCursor struct{ b []byte }

func (c *tlsCursor) take(n int) ([]byte, bool) {
	if n < 0 || n > len(c.b) {
		return nil, false
	}
	head := c.b[:n]
	c.b = c.b[n:]
	return head, true
}

func (c *tlsCursor) uint(width int) (int, bool) {
	b, ok := c.take(width)
	if !ok {
		return 0, false
	}
	v := 0
	for _, x := range b {
		v = v<<8 | int(x)
	}
	return v, true
}

func (c *tlsCursor) vable(width int) ([]byte, bool) {
	n, ok := c.uint(width)
	if !ok {
		return nil, false
	}
	return c.take(n)
}

// ServerHello is what a ServerHello says: the suite, the group with its key
// bytes, whether key_share came before supported_versions (the order the site
// chose), and the random.
type ServerHello struct {
	Suite    Suite
	Group    uint16
	Key      []byte
	KeyFirst bool
	Random   []byte
}

// ReadServerHello reads a ServerHello message with its 4-byte header. It must
// echo session, hold nothing but supported_versions (TLS 1.3) and one
// key_share of X25519 or the hybrid group of the right length, name one of
// the three suites, and not be a HelloRetryRequest.
func ReadServerHello(message, session []byte) (ServerHello, bool) {
	outer := &tlsCursor{b: message}
	kind, ok1 := outer.uint(1)
	body, ok2 := outer.vable(3)
	if !ok1 || !ok2 || kind != 2 || len(outer.b) != 0 {
		return ServerHello{}, false
	}
	hello := &tlsCursor{b: body}
	version, ok1 := hello.uint(2)
	random, ok2 := hello.take(32)
	echoed, ok3 := hello.vable(1)
	id, ok4 := hello.uint(2)
	compression, ok5 := hello.uint(1)
	extBytes, ok6 := hello.vable(2)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) || version != 0x0303 || len(hello.b) != 0 {
		return ServerHello{}, false
	}
	if compression != 0 || string(echoed) != string(session) {
		return ServerHello{}, false
	}
	var rnd [32]byte
	copy(rnd[:], random)
	if rnd == helloRetry {
		return ServerHello{}, false
	}
	suite, ok := SuiteFromID(uint16(id))
	if !ok {
		return ServerHello{}, false
	}
	exts := &tlsCursor{b: extBytes}
	versions := false
	var group uint16
	var key []byte
	haveShare, keyFirst := false, false
	for len(exts.b) != 0 {
		ek, ok1 := exts.uint(2)
		data, ok2 := exts.vable(2)
		if !ok1 || !ok2 {
			return ServerHello{}, false
		}
		switch {
		case ek == 43 && !versions && string(data) == string([]byte{3, 4}):
			versions = true
		case ek == 51 && !haveShare:
			keyFirst = !versions
			entry := &tlsCursor{b: data}
			g, ok1 := entry.uint(2)
			k, ok2 := entry.vable(2)
			if !ok1 || !ok2 || len(entry.b) != 0 {
				return ServerHello{}, false
			}
			want := 32
			if uint16(g) == GroupHybrid {
				want = kemCiphertext + 32
			} else if uint16(g) != GroupX25519 {
				return ServerHello{}, false
			}
			if len(k) != want {
				return ServerHello{}, false
			}
			group, key, haveShare = uint16(g), k, true
		default:
			return ServerHello{}, false
		}
	}
	if !versions || !haveShare {
		return ServerHello{}, false
	}
	return ServerHello{Suite: suite, Group: group, Key: key, KeyFirst: keyFirst, Random: random}, true
}

// WriteServerHello writes a ServerHello with its 4-byte header, in the
// extension order keyFirst says.
func WriteServerHello(session []byte, s Suite, group uint16, keyFirst bool, random [32]byte, key []byte) []byte {
	versions := []byte{0, 43, 0, 2, 3, 4}
	share := []byte{0, 51}
	share = append(share, byte((len(key)+4)>>8), byte(len(key)+4))
	share = append(share, byte(group>>8), byte(group))
	share = append(share, byte(len(key)>>8), byte(len(key)))
	share = append(share, key...)
	var exts []byte
	if keyFirst {
		exts = append(append(exts, share...), versions...)
	} else {
		exts = append(append(exts, versions...), share...)
	}
	var body []byte
	body = append(body, 3, 3)
	body = append(body, random[:]...)
	body = append(body, byte(len(session)))
	body = append(body, session...)
	id := s.ID()
	body = append(body, byte(id>>8), byte(id))
	body = append(body, 0)
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)
	msg := []byte{2, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...)
}
