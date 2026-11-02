package stealth

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// The keys.
//
// Every device has a transport secret of its own: 32 random bytes the panel
// draws when the device is made and hands only to that device, inside its
// profile. It is deliberately not derived from the device's WireGuard key. The
// WireGuard key proves who the device is to the tunnel; the transport secret
// only lets its packets past the wrapping, and a leak of one says nothing
// about the other.
//
// Everything a transport uses is derived from that secret and the tunnel's
// anchor -- the tunnel's WireGuard public key, which every profile already
// carries -- so one device's keys for one tunnel are worthless on any other.

// SecretLen is the length of a device's transport secret.
const SecretLen = 32

// TagLen is the length of a tag: what the server looks a device up by.
const TagLen = 16

// Secret is a device's transport secret.
type Secret [SecretLen]byte

// Anchor is the tunnel's WireGuard public key, which every derived key is
// bound to.
type Anchor [32]byte

// Tag is a device's identifier on the wire. A stream proof carries the
// device's constant tag; a UDP opening carries the tag of the current
// two-minute epoch, so a UDP device is not one recurring value.
type Tag [TagLen]byte

// EpochLength is how long one UDP tag lasts.
const EpochLength = 120 * time.Second

// Epoch is the epoch an instant falls in.
func Epoch(t time.Time) uint64 { return uint64(t.Unix()) / uint64(EpochLength/time.Second) }

const label = "w-ui stealth v1 "

// NewSecret draws a device's transport secret.
func NewSecret() (Secret, error) {
	var s Secret
	if _, err := rand.Read(s[:]); err != nil {
		return Secret{}, fmt.Errorf("stealth: read entropy: %w", err)
	}
	return s, nil
}

// String is the secret in standard base64, as profiles and the database keep
// it.
func (s Secret) String() string { return base64.StdEncoding.EncodeToString(s[:]) }

// ParseSecret reads a secret written by String.
func ParseSecret(v string) (Secret, error) {
	var s Secret
	if err := parse32(v, s[:]); err != nil {
		return Secret{}, fmt.Errorf("stealth: transport secret: %w", err)
	}
	return s, nil
}

// ParseAnchor reads a tunnel's WireGuard public key.
func ParseAnchor(v string) (Anchor, error) {
	var a Anchor
	if err := parse32(v, a[:]); err != nil {
		return Anchor{}, fmt.Errorf("stealth: tunnel public key: %w", err)
	}
	return a, nil
}

func parse32(v string, dst []byte) error {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return err
	}
	if len(b) != 32 {
		return errors.New("not 32 bytes")
	}
	copy(dst, b)
	return nil
}

// DeviceKeys is everything one device uses with one tunnel.
type DeviceKeys struct {
	// Tag is the device's constant tag, which a stream proof carries.
	Tag Tag
	// tagKey makes the per-epoch UDP tags.
	tagKey []byte
	// Bind proves, inside a stream proof, that whoever made it holds the
	// secret and did not just copy a tag seen elsewhere.
	Bind []byte
	// UDP is the root of every UDP session key.
	UDP []byte
	// Stream is mixed into every stream session's keys, beside what the
	// proof's HPKE context exports, so that a stream's keys need the
	// device's secret as well as the proof's ephemeral key.
	Stream []byte
}

// Derive works out a device's keys for a tunnel.
func Derive(secret Secret, anchor Anchor) DeviceKeys {
	prk, err := hkdf.Extract(sha256.New, secret[:], []byte(label+"device"))
	if err != nil {
		panic(err) // HKDF over SHA-256 with these lengths cannot fail
	}
	k := DeviceKeys{
		tagKey: expand(prk, "tag", anchor[:]),
		Bind:   expand(prk, "bind", anchor[:]),
		UDP:    expand(prk, "udp", anchor[:]),
		Stream: expand(prk, "stream", anchor[:]),
	}
	copy(k.Tag[:], mac(k.tagKey, []byte("device")))
	return k
}

// EpochTag is the device's UDP tag for one epoch.
func (k DeviceKeys) EpochTag(epoch uint64) Tag {
	var msg [5 + 8]byte
	copy(msg[:], "epoch")
	binary.BigEndian.PutUint64(msg[5:], epoch)
	var t Tag
	copy(t[:], mac(k.tagKey, msg[:]))
	return t
}

// HourTag is the device's mirror tag for one hour: it names the device
// inside the proof a client hides in its ClientHello. Like the UDP epoch tag
// it has a time step, but of one hour, so a borrowed-site hello stays valid
// across a clock a little off.
func (k DeviceKeys) HourTag(hour uint64) [TagLen]byte {
	var msg [12 + 8]byte
	copy(msg[:], "faketls hour")
	binary.BigEndian.PutUint64(msg[12:], hour)
	var t [TagLen]byte
	copy(t[:], mac(k.tagKey, msg[:]))
	return t
}

// MaskKey is the tunnel's UDP mask key. It is public -- anyone with a profile
// for the tunnel can work it out -- and is there only so that a tag on the
// wire changes with every packet: a tag XORed with a mask drawn from the
// packet's own random salt is never the same twice.
func MaskKey(anchor Anchor) []byte {
	k, err := hkdf.Key(sha256.New, anchor[:], []byte(label+"mask"), "", 32)
	if err != nil {
		panic(err)
	}
	return k
}

// mask is what a tag is XORed with in a UDP opening that carries salt.
func mask(maskKey, salt []byte) Tag {
	var t Tag
	copy(t[:], mac(maskKey, salt))
	return t
}

func expand(prk []byte, what string, anchor []byte) []byte {
	k, err := hkdf.Expand(sha256.New, prk, what+"\x00"+string(anchor), 32)
	if err != nil {
		panic(err)
	}
	return k
}

func mac(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
