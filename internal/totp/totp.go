// Package totp implements the six-digit codes an authenticator app produces.
//
// The panel is reachable from the internet and its password is the only thing
// between an attacker and every customer's configuration, the server's keys, and
// the ability to hand themselves free service. A second factor turns a leaked or
// guessed password from a breach into a nuisance.
//
// RFC 6238 with SHA-1 and six digits, because that is what every authenticator
// app implements. Choosing something stronger here would mean codes that Google
// Authenticator cannot produce, which is not stronger in practice.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Step is the code lifetime every authenticator assumes.
	Step = 30 * time.Second

	// Digits in a code.
	Digits = 6

	// Skew is how many steps either side of now are accepted.
	//
	// One step, so a code is valid for about ninety seconds. Phone clocks drift
	// and people type slowly; without this a correct code is rejected often
	// enough that operators turn the whole feature off.
	Skew = 1

	// secretBytes is the shared secret's length. Twenty is the RFC's
	// recommendation for SHA-1 and what authenticator apps expect.
	secretBytes = 20
)

// NewSecret generates a shared secret in the base32 form apps read.
func NewSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	// No padding: authenticator apps and QR readers choke on '=' characters.
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// URI builds the otpauth link an authenticator app scans.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(int(Step.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Generate returns the code for a moment in time.
func Generate(secret string, at time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return code(key, uint64(at.Unix())/uint64(Step.Seconds())), nil
}

// Validate reports whether a code is correct for roughly now.
//
// The comparison is constant-time. A timing difference would let an attacker
// learn a code digit by digit, which for six digits is the difference between a
// million guesses and sixty.
func Validate(secret, entered string, now time.Time) bool {
	entered = strings.TrimSpace(entered)
	if len(entered) != Digits {
		return false
	}
	key, err := decode(secret)
	if err != nil {
		return false
	}

	counter := uint64(now.Unix()) / uint64(Step.Seconds())
	for delta := -Skew; delta <= Skew; delta++ {
		c := counter
		if delta < 0 {
			if c < uint64(-delta) {
				continue
			}
			c -= uint64(-delta)
		} else {
			c += uint64(delta)
		}
		if subtle.ConstantTimeCompare([]byte(code(key, c)), []byte(entered)) == 1 {
			return true
		}
	}
	return false
}

func decode(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	// Accept a padded secret too: people paste them from all sorts of places.
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("totp: secret is not valid base32: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("totp: secret is empty")
	}
	return key, nil
}

// code is the RFC 6238 truncation of an HMAC over the counter.
func code(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < Digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, value%mod)
}
