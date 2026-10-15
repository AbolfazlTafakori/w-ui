package routing

import "testing"

// The sign-in a profile carries inline is read, Windows line endings or
// not; a profile without one, or with half of one, gives none.
func TestTheSignInAProfileCarriesIsRead(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		p := "client" + nl + "remote vpn.example.com 1194" + nl + "auth-user-pass" + nl +
			"<auth-user-pass>" + nl + "s1-device-1" + nl + "Secret123" + nl + "</auth-user-pass>" + nl
		u, pw, ok := InlineAuthUserPass(p)
		if !ok || u != "s1-device-1" || pw != "Secret123" {
			t.Fatalf("%q: %q %q %v", nl, u, pw, ok)
		}
	}
	if _, _, ok := InlineAuthUserPass("client\nauth-user-pass\n"); ok {
		t.Fatal("a sign-in where there is none")
	}
	if _, _, ok := InlineAuthUserPass("<auth-user-pass>\nonly-a-name\n</auth-user-pass>\n"); ok {
		t.Fatal("half a sign-in taken")
	}
}
