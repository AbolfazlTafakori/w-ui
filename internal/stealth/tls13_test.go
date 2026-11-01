package stealth

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 8448 section 3, "Simple 1-RTT Handshake": the key schedule of
// TLS_AES_128_GCM_SHA256 with X25519.
func TestTLSKeyScheduleRFC8448(t *testing.T) {
	shared := unhex(t, "8bd4054fb55b9d63fdfbacf9f04b9f0d35e6d63f537563efd46272900f89492d")
	helloHash := unhex(t, "860c06edc07858ee8e78f0e7428c58edd6b43f2ca3e6e95f02ed063cf0e1cad8")
	s := SuiteAES128
	sec := HandshakeSecrets(s, shared, helloHash)
	if !bytes.Equal(sec.Client, unhex(t, "b3eddb126e067f35a780b3abf45e2d8f3b1a950738f52e9600746a0e27a55a21")) {
		t.Fatal("c hs traffic")
	}
	if !bytes.Equal(sec.Server, unhex(t, "b67b7d690cc16c4e75e54213cb2d37b4e9c912bcded9105d42befd59d391ad38")) {
		t.Fatal("s hs traffic")
	}
	if !bytes.Equal(expandLabel(s, sec.Server, "key", nil, 16), unhex(t, "3fce516009c21727d0f2e4e86ee403bc")) {
		t.Fatal("server key")
	}
	if !bytes.Equal(expandLabel(s, sec.Server, "iv", nil, 12), unhex(t, "5d313eb2671276ee13000b30")) {
		t.Fatal("server iv")
	}
	if !bytes.Equal(sec.Next, unhex(t, "43de77e0c77713859a944db9db2590b53190a65b3ee2e4f12dd7a0bb7ce254b4")) {
		t.Fatal("derived salt")
	}
	throughVerify := unhex(t, "edb7725fa7a3473b031ec8ef65a24854939001 38a2b9129140 7d7951a06110ed")
	if !bytes.Equal(Finished(s, sec.Server, throughVerify), unhex(t, "9b9b141d906337fbd2cbdce71df4deda4ab42c309572cb7fffee5454b78f0718")) {
		t.Fatal("server finished")
	}
	throughFinished := unhex(t, "9608102a0f1ccc6db6250b7b7e417b1a000eaada3daae4777a7686c9ff83df13")
	client, server := ApplicationSecrets(s, sec.Next, throughFinished)
	if !bytes.Equal(client, unhex(t, "9e40646ce79a7f9dc05af8889bce6552875afa0b06df0087f792ebb7c17504a5")) {
		t.Fatal("c ap traffic")
	}
	if !bytes.Equal(server, unhex(t, "a11af9f05531f856ad47116b45a9503282 04b4f44bfb6b3a4b4f1f3fcb631643")) {
		t.Fatal("s ap traffic")
	}
}

func TestTLSRecordRoundTrip(t *testing.T) {
	for _, s := range []Suite{SuiteAES128, SuiteAES256, SuiteChacha} {
		secret := bytes.Repeat([]byte{7}, s.HashLen())
		seal, _ := NewTLSKeys(s, secret)
		open, _ := NewTLSKeys(s, secret)
		out, ok := seal.Seal(22, []byte("hello"), 9, nil)
		if !ok {
			t.Fatalf("suite %d: seal failed", s)
		}
		if len(out) != TLSHeader+5+1+9+TLSTag {
			t.Fatalf("suite %d: record length %d", s, len(out))
		}
		header := out[:TLSHeader]
		body := append([]byte(nil), out[TLSHeader:]...)
		kind, content, ok := open.Open(header, body)
		if !ok || kind != 22 || string(content) != "hello" {
			t.Fatalf("suite %d: open gave %d %q %v", s, kind, content, ok)
		}
		// The same record does not open twice (the number moved on).
		again := append([]byte(nil), out[TLSHeader:]...)
		if _, _, ok := open.Open(header, again); ok {
			t.Fatalf("suite %d: a record opened twice", s)
		}
		// A record that would not fit is refused.
		if _, ok := seal.Seal(23, make([]byte, 16384), 1, out); ok {
			t.Fatalf("suite %d: an over-long record was sealed", s)
		}
	}
}

func TestServerHelloReadWrite(t *testing.T) {
	session := bytes.Repeat([]byte{5}, 32)
	var random [32]byte
	for i := range random {
		random[i] = 9
	}
	for _, c := range []struct {
		group uint16
		key   []byte
	}{{GroupX25519, bytes.Repeat([]byte{1}, 32)}, {GroupHybrid, bytes.Repeat([]byte{2}, kemCiphertext+32)}} {
		for _, keyFirst := range []bool{false, true} {
			msg := WriteServerHello(session, SuiteAES256, c.group, keyFirst, random, c.key)
			sh, ok := ReadServerHello(msg, session)
			if !ok {
				t.Fatalf("group %x keyFirst %v: did not read", c.group, keyFirst)
			}
			if sh.Suite != SuiteAES256 || sh.Group != c.group || !bytes.Equal(sh.Key, c.key) || sh.KeyFirst != keyFirst {
				t.Fatalf("group %x: read back wrong: %+v", c.group, sh)
			}
			if _, ok := ReadServerHello(msg, bytes.Repeat([]byte{6}, 32)); ok {
				t.Fatal("a wrong session echoed")
			}
			if _, ok := ReadServerHello(msg[:len(msg)-1], session); ok {
				t.Fatal("a truncated hello read")
			}
		}
	}
	// A HelloRetryRequest and an unknown group are refused.
	if _, ok := ReadServerHello(WriteServerHello(session, SuiteAES128, GroupX25519, false, helloRetry, bytes.Repeat([]byte{1}, 32)), session); ok {
		t.Fatal("a HelloRetryRequest read")
	}
}
