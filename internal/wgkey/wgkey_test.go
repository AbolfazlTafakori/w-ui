package wgkey

import "testing"

func TestGeneratedKeysAreDistinctAndClamped(t *testing.T) {
	a, err := GeneratePrivate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GeneratePrivate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a == b {
		t.Fatal("two generated private keys are identical")
	}

	if a[0]&7 != 0 {
		t.Errorf("low three bits of byte 0 are set: %08b", a[0])
	}
	if a[31]&128 != 0 {
		t.Errorf("high bit of byte 31 is set: %08b", a[31])
	}
	if a[31]&64 == 0 {
		t.Errorf("bit 254 is not set: %08b", a[31])
	}
}

func TestPublicKeyIsDeterministic(t *testing.T) {
	priv, err := GeneratePrivate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	first, err := priv.Public()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	second, err := priv.Public()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if first != second {
		t.Error("deriving the public key twice gave different results")
	}
	if first == Key(priv) {
		t.Error("public key equals the private key")
	}
}

func TestRoundTripsThroughBase64(t *testing.T) {
	pair, err := NewPair()
	if err != nil {
		t.Fatalf("new pair: %v", err)
	}

	for name, key := range map[string]Key{
		"private":   pair.Private,
		"public":    pair.Public,
		"preshared": pair.Preshared,
	} {
		encoded := key.String()
		if len(encoded) != 44 {
			t.Errorf("%s key encodes to %d chars, want 44", name, len(encoded))
		}
		decoded, err := Parse(encoded)
		if err != nil {
			t.Fatalf("parse %s key: %v", name, err)
		}
		if decoded != key {
			t.Errorf("%s key did not survive the round trip", name)
		}
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	for _, in := range []string{"", "not base64!", "c2hvcnQ="} {
		if _, err := Parse(in); err == nil {
			t.Errorf("parse(%q) succeeded, want an error", in)
		}
	}
}
