package radiusd

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 2759, section 9.2, and RFC 3079, section 3.5.3: the protocol's own
// worked example, every intermediate value of it.
func TestMSCHAPv2MatchesTheRFCsExample(t *testing.T) {
	const user, pass = "User", "clientPass"
	auth := unhex(t, "5B 5D 7C 7D 7B 3F 2F 3E 3C 2C 60 21 32 26 26 28")
	peer := unhex(t, "21 40 23 24 25 5E 26 2A 28 29 5F 2B 3A 33 7C 7E")

	if got := challengeHash(peer, auth, user); !bytes.Equal(got, unhex(t, "D0 2E 43 86 BC E9 12 26")) {
		t.Fatalf("challenge %X", got)
	}
	if got := ntPasswordHash(pass); !bytes.Equal(got, unhex(t, "44 EB BA 8D 53 12 B8 D6 11 47 44 11 F5 69 89 AE")) {
		t.Fatalf("password hash %X", got)
	}
	nt := ntResponse(auth, peer, user, pass)
	if !bytes.Equal(nt, unhex(t, "82 30 9E CD 8D 70 8B 5E A0 8F AA 39 81 CD 83 54 42 33 11 4A 3D 85 D6 DF")) {
		t.Fatalf("NT response %X", nt)
	}
	if got := hashNtPasswordHash(ntPasswordHash(pass)); !bytes.Equal(got, unhex(t, "41 C0 0C 58 4B D2 D9 1C 40 17 A2 A1 2F A5 9F 3F")) {
		t.Fatalf("hash of the hash %X", got)
	}
	if got := authenticatorResponse(pass, nt, peer, auth, user); got != "S=407A5589115FD0D6209F510FE9C04566932CDA56" {
		t.Fatalf("authenticator response %s", got)
	}
	if got := masterKey(pass, nt); !bytes.Equal(got, unhex(t, "FD EC E3 71 7A 8C 83 8C B3 88 E5 27 AE 3C DD 31")) {
		t.Fatalf("master key %X", got)
	}
	// RFC 3079's example of the 128-bit send start key, worked from the
	// server's side (IsServer TRUE): the server's send key.
	recv, send := serverKeys(pass, nt)
	if !bytes.Equal(send, unhex(t, "8B 7C DC 14 9B 99 3A 1B A1 18 CB 15 3F 56 DC CB")) {
		t.Fatalf("server send key %X", send)
	}
	if bytes.Equal(recv, send) {
		t.Fatal("the two keys are the same")
	}
	// A domain before the username is not part of the hash.
	if !bytes.Equal(challengeHash(peer, auth, `EXAMPLE\User`), challengeHash(peer, auth, user)) {
		t.Fatal("the domain was hashed")
	}
}
