// Package wgkey generates and parses WireGuard keys.
//
// WireGuard identifies a peer by a Curve25519 key pair and nothing else: there
// is no username, no password and no certificate. Generating a key pair here is
// therefore the whole of creating a customer's identity.
package wgkey

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// KeyLen is the length of every WireGuard key in bytes.
const KeyLen = 32

// Key is a Curve25519 private or public key, or a pre-shared key.
type Key [KeyLen]byte

// GeneratePrivate returns a new private key.
//
// The clamping matches the Curve25519 specification: clearing the three low
// bits and forcing bit 254 keeps the scalar in the right subgroup, which is
// what makes the exchange safe against small-subgroup attacks.
func GeneratePrivate() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("wgkey: read entropy: %w", err)
	}
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	return k, nil
}

// GeneratePresharedKey returns a random symmetric key.
//
// The pre-shared key is an optional extra layer mixed into the handshake. It is
// generated for every peer because it costs nothing and hardens the tunnel
// against an attacker recording traffic now to break later.
func GeneratePresharedKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("wgkey: read entropy: %w", err)
	}
	return k, nil
}

// Public derives the public key for a private key.
func (k Key) Public() (Key, error) {
	pub, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return Key{}, fmt.Errorf("wgkey: derive public key: %w", err)
	}
	var out Key
	copy(out[:], pub)
	return out, nil
}

// String returns the standard base64 encoding used in WireGuard configs.
func (k Key) String() string { return base64.StdEncoding.EncodeToString(k[:]) }

// IsZero reports whether the key is unset.
func (k Key) IsZero() bool { return k == Key{} }

// Parse decodes a base64 key.
func Parse(s string) (Key, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("wgkey: decode %q: %w", s, err)
	}
	if len(raw) != KeyLen {
		return Key{}, fmt.Errorf("wgkey: key is %d bytes, want %d", len(raw), KeyLen)
	}
	var k Key
	copy(k[:], raw)
	return k, nil
}

// Pair is a generated key pair with its pre-shared key.
type Pair struct {
	Private   Key
	Public    Key
	Preshared Key
}

// NewPair generates a private key, its public key and a pre-shared key.
func NewPair() (Pair, error) {
	priv, err := GeneratePrivate()
	if err != nil {
		return Pair{}, err
	}
	pub, err := priv.Public()
	if err != nil {
		return Pair{}, err
	}
	psk, err := GeneratePresharedKey()
	if err != nil {
		return Pair{}, err
	}
	return Pair{Private: priv, Public: pub, Preshared: psk}, nil
}
