package stealth

import (
	"net/http"
	"strings"
)

// The cookie of bare.
//
// bare has no TLS between the device and the front, so anything in the
// request target is in every log on the way, the front's own included. Its
// token travels in a cookie instead, under a name the panel draws for the
// transport, and the target is the path alone.
//
// The cookie is read strictly, so that a request has one reading only: it
// has exactly one Cookie field, the field is name=value pairs joined by
// "; ", every name is an HTTP token, the transport's name is among them
// exactly once, and its value is one or more characters of A-Z a-z 0-9 - _,
// which is what a token is. Anything else is a request like any other's.

// CookieMax is the longest name a bare cookie may have.
const CookieMax = 32

// CookieName says whether name may name a bare cookie: 1 to CookieMax
// characters of A-Z a-z 0-9 - _.
func CookieName(name string) bool {
	return name != "" && len(name) <= CookieMax && strings.IndexFunc(name, notTokenChar) < 0
}

// CookieToken is the value of the cookie name in h, under the rules above,
// or false.
func CookieToken(h http.Header, name string) (string, bool) {
	fields := h.Values("Cookie")
	if len(fields) != 1 {
		return "", false
	}
	var (
		value string
		seen  bool
	)
	for _, pair := range strings.Split(fields[0], "; ") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || !httpToken(k) {
			return "", false // a pair that is none, a blank, or a separator at either end
		}
		if k == name {
			if seen {
				return "", false
			}
			value, seen = v, true
		}
	}
	if !seen || value == "" || strings.IndexFunc(value, notTokenChar) >= 0 {
		return "", false
	}
	return value, true
}

// notTokenChar is anything but a character of base64url, which is what a
// token and a cookie's name are made of.
func notTokenChar(c rune) bool {
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
}

// httpToken is an HTTP token (RFC 9110): what a cookie's name may be.
func httpToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
