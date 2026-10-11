package radiusd

import (
	"crypto/des"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // MS-CHAPv2 is defined on MD4.
)

// MS-CHAPv2 (RFC 2759) and the keys it yields (RFC 3079): what a device
// proves its password with, and what the server proves its own with in
// return. MD4 and DES are what the protocol is made of; they are not chosen
// here.

var (
	magic1 = []byte("Magic server to client signing constant")
	magic2 = []byte("Pad to make it do more than one iteration")

	magicMaster = []byte("This is the MPPE Master Key")
	// From the server's side: what it receives with is the client's send
	// key, and what it sends with the client's receive key.
	magicRecvServer = []byte("On the client side, this is the send key; on the server side, it is the receive key.")
	magicSendServer = []byte("On the client side, this is the receive key; on the server side, it is the send key.")
	shsPad1         = make([]byte, 40)
	shsPad2         = []byte(strings.Repeat("\xf2", 40))
)

// ntPasswordHash is MD4 of the password in UTF-16, little-endian.
func ntPasswordHash(password string) []byte {
	u := utf16.Encode([]rune(password))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	h := md4.New()
	h.Write(b)
	return h.Sum(nil)
}

func hashNtPasswordHash(hash []byte) []byte {
	h := md4.New()
	h.Write(hash)
	return h.Sum(nil)
}

// challengeHash is the first 8 bytes of SHA1(peer challenge, authenticator
// challenge, username) -- the username without any domain.
func challengeHash(peer, auth []byte, username string) []byte {
	if i := strings.LastIndexByte(username, '\\'); i >= 0 {
		username = username[i+1:]
	}
	h := sha1.New()
	h.Write(peer)
	h.Write(auth)
	h.Write([]byte(username))
	return h.Sum(nil)[:8]
}

// challengeResponse is the 24-byte answer: the challenge encrypted with
// three DES keys cut from the password hash.
func challengeResponse(challenge, hash []byte) []byte {
	z := make([]byte, 21)
	copy(z, hash)
	out := make([]byte, 0, 24)
	for i := 0; i < 3; i++ {
		block, _ := des.NewCipher(desKey(z[7*i : 7*i+7]))
		buf := make([]byte, 8)
		block.Encrypt(buf, challenge)
		out = append(out, buf...)
	}
	return out
}

// desKey spreads 7 bytes over 8, the lowest bit of each left for parity.
func desKey(k []byte) []byte {
	return []byte{
		k[0] & 0xfe,
		(k[0]<<7 | k[1]>>1) & 0xfe,
		(k[1]<<6 | k[2]>>2) & 0xfe,
		(k[2]<<5 | k[3]>>3) & 0xfe,
		(k[3]<<4 | k[4]>>4) & 0xfe,
		(k[4]<<3 | k[5]>>5) & 0xfe,
		(k[5]<<2 | k[6]>>6) & 0xfe,
		k[6] << 1,
	}
}

// ntResponse is what a device with this password answers.
func ntResponse(auth, peer []byte, username, password string) []byte {
	return challengeResponse(challengeHash(peer, auth, username), ntPasswordHash(password))
}

// authenticatorResponse is the server's proof it knows the password too:
// "S=" and 40 hex digits.
func authenticatorResponse(password string, nt, peer, auth []byte, username string) string {
	hh := hashNtPasswordHash(ntPasswordHash(password))
	d := sha1.New()
	d.Write(hh)
	d.Write(nt)
	d.Write(magic1)
	digest := d.Sum(nil)
	d = sha1.New()
	d.Write(digest)
	d.Write(challengeHash(peer, auth, username))
	d.Write(magic2)
	return "S=" + strings.ToUpper(hex.EncodeToString(d.Sum(nil)))
}

// masterKey is RFC 3079's GetMasterKey.
func masterKey(password string, nt []byte) []byte {
	hh := hashNtPasswordHash(ntPasswordHash(password))
	h := sha1.New()
	h.Write(hh)
	h.Write(nt)
	h.Write(magicMaster)
	return h.Sum(nil)[:16]
}

// sessionKey is RFC 3079's GetAsymmetricStartKey, 16 bytes.
func sessionKey(master, magic []byte) []byte {
	h := sha1.New()
	h.Write(master)
	h.Write(shsPad1)
	h.Write(magic)
	h.Write(shsPad2)
	return h.Sum(nil)[:16]
}

// serverKeys are the keys the server -- the authenticator -- receives and
// sends with, which RADIUS hands the gateway as MS-MPPE-Recv-Key and
// MS-MPPE-Send-Key.
func serverKeys(password string, nt []byte) (recv, send []byte) {
	m := masterKey(password, nt)
	return sessionKey(m, magicRecvServer), sessionKey(m, magicSendServer)
}
