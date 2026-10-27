package stealth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func newProofPair(t *testing.T) (*ProofClient, *ProofServer, TransportKey) {
	t.Helper()
	key, err := NewTransportKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewProofClient(testSecret, testAnchor, WS, key.Public())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewProofServer(testAnchor, WS, key)
	if err != nil {
		t.Fatal(err)
	}
	return c, s, key
}

func TestAProofOpensASessionWithTheSameKeysOnBothSides(t *testing.T) {
	c, s, _ := newProofPair(t)
	token, ck, err := c.Token(testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < TokenTextMin || len(token) > TokenTextMax {
		t.Fatalf("a token of %d characters, not %d..%d", len(token), TokenTextMin, TokenTextMax)
	}
	p, err := s.Open(token)
	if err != nil {
		t.Fatal(err)
	}
	dev := Derive(testSecret, testAnchor)
	if p.Tag != dev.Tag || p.Timestamp != uint64(testNow.Unix()) {
		t.Fatalf("the proof says tag %x at %d", p.Tag, p.Timestamp)
	}
	sk, err := s.Check(&p, &dev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ck.Up, sk.Up) || !bytes.Equal(ck.Down, sk.Down) || bytes.Equal(sk.Up, sk.Down) {
		t.Fatal("the two sides do not have the same two keys")
	}
}

func TestEveryTokenAndEverySessionIsFresh(t *testing.T) {
	c, _, _ := newProofPair(t)
	a, ka, _ := c.Token(testNow)
	b, kb, _ := c.Token(testNow)
	if a == b || bytes.Equal(ka.Up, kb.Up) {
		t.Fatal("two tokens, or their keys, are the same")
	}
	if strings.ContainsAny(a, "+/=") {
		t.Fatal("a token is not unpadded base64url")
	}
}

func TestATokenIsNoProofWithoutTheSecret(t *testing.T) {
	c, s, _ := newProofPair(t)
	token, _, _ := c.Token(testNow)
	p, err := s.Open(token)
	if err != nil {
		t.Fatal(err)
	}
	other := Derive(Secret(bytes.Repeat([]byte{0x33}, 32)), testAnchor)
	if _, err := s.Check(&p, &other); err != ErrProof {
		t.Fatal("a proof checked against another device's keys")
	}
	if _, err := s.Check(&p, nil); err != ErrProof {
		t.Fatal("a proof with no device was taken")
	}
	// A tag copied into a token of somebody else's making: the binder is
	// worked out under that token's own export, which the copier does not
	// have the secret to match.
	forged := Derive(Secret(bytes.Repeat([]byte{0x44}, 32)), testAnchor)
	forged.Tag = Derive(testSecret, testAnchor).Tag
	fc := &ProofClient{keys: forged, server: c.server, info: c.info}
	ft, _, _ := fc.Token(testNow)
	fp, err := s.Open(ft)
	if err != nil {
		t.Fatal(err)
	}
	dev := Derive(testSecret, testAnchor)
	if fp.Tag != dev.Tag {
		t.Fatal("the forged tag did not come through")
	}
	if _, err := s.Check(&fp, &dev); err != ErrProof {
		t.Fatal("a copied tag passed for a proof")
	}
}

func TestAProofIsBoundToItsTransportItsTunnelAndItsKey(t *testing.T) {
	c, _, key := newProofPair(t)
	token, _, _ := c.Token(testNow)
	otherKey, _ := NewTransportKey()
	for name, s := range map[string]func() (*ProofServer, error){
		"another transport": func() (*ProofServer, error) { return NewProofServer(testAnchor, TLS, key) },
		"another tunnel":    func() (*ProofServer, error) { return NewProofServer(Anchor(bytes.Repeat([]byte{9}, 32)), WS, key) },
		"another key":       func() (*ProofServer, error) { return NewProofServer(testAnchor, WS, otherKey) },
	} {
		srv, err := s()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := srv.Open(token); err != ErrProof {
			t.Errorf("%s opened the token", name)
		}
	}
}

func TestTheKeyBeforeStillOpensAProof(t *testing.T) {
	c, _, old := newProofPair(t)
	token, _, _ := c.Token(testNow)
	current, _ := NewTransportKey()
	s, err := NewProofServer(testAnchor, WS, current, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(token); err != nil {
		t.Fatal("a token to the key before was refused")
	}
}

func TestEveryChangedByteOfATokenIsRefused(t *testing.T) {
	c, s, _ := newProofPair(t)
	token, _, _ := c.Token(testNow)
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	for i := range raw {
		b := bytes.Clone(raw)
		b[i] ^= 0x01
		if _, err := s.Open(base64.RawURLEncoding.EncodeToString(b)); err == nil {
			t.Fatalf("byte %d changed and the token still opened", i)
		}
	}
	for _, bad := range []string{"", "x", token[:len(token)-1] + "*", token + "AAAA", strings.Repeat("A", TokenTextMax+1)} {
		if _, err := s.Open(bad); err != ErrProof {
			t.Errorf("%q was not refused", bad)
		}
	}
}

func TestATransportKeyReadsBackAsItWasWritten(t *testing.T) {
	k, _ := NewTransportKey()
	back, err := ParseTransportKey(k.String())
	if err != nil {
		t.Fatal(err)
	}
	if back.Public() != k.Public() {
		t.Fatal("a key read back has another public half")
	}
	if _, err := ParseTransportKey("bm90IGEga2V5"); err == nil {
		t.Fatal("a short key was read")
	}
	if _, err := NewProofClient(testSecret, testAnchor, WS, "AAAA"); err == nil {
		t.Fatal("a client took a server key that is none")
	}
}
