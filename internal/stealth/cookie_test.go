package stealth

import (
	"net/http"
	"strings"
	"testing"
)

func TestACookieNameIsBase64urlCharactersAlone(t *testing.T) {
	for name, want := range map[string]bool{
		"sid": true, "a": true, "Ab_9-z": true, strings.Repeat("x", CookieMax): true,
		"": false, strings.Repeat("x", CookieMax+1): false, "a b": false, "a=b": false, "a;b": false, "a.b": false, "ñ": false,
	} {
		if CookieName(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}

// A request has one reading only: one Cookie field, well formed, the name
// once, a value a token could be.
func TestATokenIsReadFromItsCookieAlone(t *testing.T) {
	const tok = "ZC1KPdsNS2QaCxRE-bxz_sF5"
	for _, tc := range []struct {
		fields []string
		want   string
	}{
		{[]string{"sid=" + tok}, tok},
		{[]string{"_ga=GA1.2.3; sid=" + tok + "; theme=dark"}, tok},
		{[]string{"theme=dark; sid=" + tok}, tok},
		{[]string{"a=; sid=" + tok}, tok}, // another cookie may be empty
		{nil, ""},
		{[]string{"sid=" + tok, "x=1"}, ""},              // two fields
		{[]string{"sid=" + tok + "; sid=" + tok}, ""},    // the name twice
		{[]string{"sid=" + tok + ";theme=dark"}, ""},     // a separator without its blank
		{[]string{"sid=" + tok + "; "}, ""},              // a separator at the end
		{[]string{"; sid=" + tok}, ""},                   // and at the start
		{[]string{"sid=" + tok + ";  theme=dark"}, ""},   // two blanks
		{[]string{"sid=" + tok + "; theme"}, ""},         // a pair that is none
		{[]string{"sid=\"" + tok + "\""}, ""},            // a quoted value
		{[]string{"sid=" + tok + "="}, ""},               // padding
		{[]string{"sid=" + tok[:5] + " " + tok[5:]}, ""}, // a blank inside
		{[]string{"sid="}, ""},                           // empty
		{[]string{"SID=" + tok}, ""},                     // names are as they are spelled
		{[]string{"other=" + tok}, ""},
		{[]string{"s(d=1; sid=" + tok}, ""}, // a name that is no token
	} {
		h := http.Header{}
		for _, f := range tc.fields {
			h.Add("Cookie", f)
		}
		got, ok := CookieToken(h, "sid")
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%q: %q, %v", tc.fields, got, ok)
		}
	}
}

// A token is made of what a cookie's value may hold, at every length.
func TestEveryTokenFitsACookie(t *testing.T) {
	c, _, _ := newProofPair(t)
	for range 64 {
		tok, _, err := c.Token(testNow)
		if err != nil {
			t.Fatal(err)
		}
		h := http.Header{"Cookie": {"sid=" + tok}}
		if got, ok := CookieToken(h, "sid"); !ok || got != tok {
			t.Fatalf("a token did not read back from its cookie: %q", tok)
		}
	}
}
