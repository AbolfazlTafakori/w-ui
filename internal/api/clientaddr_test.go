package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// restore puts the default set back, so one test's configuration is not the
// next one's starting point.
func restore(t *testing.T) {
	t.Helper()
	saved := trustedProxies
	t.Cleanup(func() { trustedProxies = saved })
}

func req(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// The header is worthless from anybody who is not a proxy we trust.
//
// Believing it from any caller would hand an attacker both halves at once: a
// sign-in throttle they step around by changing a header, and an audit log they
// write themselves.
func TestAForwardedHeaderFromAStrangerIsIgnored(t *testing.T) {
	restore(t)

	got := clientIP(req("203.0.113.9:5555", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
		"X-Real-IP":       "1.2.3.4",
	}))
	if got != "203.0.113.9" {
		t.Errorf("clientIP() = %q; a stranger's header was believed", got)
	}
}

// From the proxy the panel is installed behind, it is the only way to know who
// is calling at all.
func TestTheProxyOnThisMachineIsBelieved(t *testing.T) {
	restore(t)

	got := clientIP(req("127.0.0.1:41234", map[string]string{
		"X-Forwarded-For": "198.51.100.7",
	}))
	if got != "198.51.100.7" {
		t.Errorf("clientIP() = %q, want the address the proxy reported", got)
	}
}

// The chain is read from the right. A client can put anything it likes at the
// left-hand end, and every proxy appends rather than replaces, so only the part
// added by machines we trust means anything.
func TestOnlyTheHopsOurOwnProxiesAddedAreBelieved(t *testing.T) {
	restore(t)

	// The client claimed to be 10.9.9.9 before it ever reached us.
	got := clientIP(req("127.0.0.1:41234", map[string]string{
		"X-Forwarded-For": "10.9.9.9, 198.51.100.7",
	}))
	if got != "198.51.100.7" {
		t.Errorf("clientIP() = %q; a client's own forged hop was believed", got)
	}
}

// A proxy that sends only X-Real-IP is still a proxy.
func TestXRealIPIsUsedWhenThereIsNoChain(t *testing.T) {
	restore(t)

	got := clientIP(req("127.0.0.1:41234", map[string]string{"X-Real-IP": "198.51.100.7"}))
	if got != "198.51.100.7" {
		t.Errorf("clientIP() = %q, want the address the proxy reported", got)
	}
}

// Nonsense in the chain stops the walk rather than being handed on as an
// address. A throttle keyed on "; DROP" or on an empty string is a throttle
// with a hole in it.
func TestGarbageInTheChainFallsBackToThePeer(t *testing.T) {
	restore(t)

	for _, bad := range []string{"not-an-address", "", "  ", "<script>"} {
		got := clientIP(req("127.0.0.1:41234", map[string]string{"X-Forwarded-For": bad}))
		if got != "127.0.0.1" {
			t.Errorf("clientIP() with %q = %q, want the peer address", bad, got)
		}
	}
}

// A proxy may write the port, and IPv6 arrives in brackets. Keying the throttle
// on "198.51.100.7:33212" would give every fresh connection from one attacker
// its own bucket, which is the throttle switched off.
func TestAPortOrBracketsDoNotCreateANewIdentity(t *testing.T) {
	restore(t)

	cases := map[string]string{
		"198.51.100.7:33212": "198.51.100.7",
		"[2001:db8::1]":      "2001:db8::1",
		"[2001:db8::1]:443":  "2001:db8::1",
	}
	for header, want := range cases {
		got := clientIP(req("127.0.0.1:41234", map[string]string{"X-Forwarded-For": header}))
		if got != want {
			t.Errorf("clientIP() with %q = %q, want %q", header, got, want)
		}
	}
}

// A deployment whose proxy is on another machine has to say so, and then it
// works the same way.
func TestAProxyElsewhereCanBeTrustedOnPurpose(t *testing.T) {
	restore(t)

	if bad := SetTrustedProxies([]string{"10.0.0.0/8"}); len(bad) > 0 {
		t.Fatalf("SetTrustedProxies rejected a valid CIDR: %v", bad)
	}

	got := clientIP(req("10.0.0.5:5555", map[string]string{"X-Forwarded-For": "198.51.100.7"}))
	if got != "198.51.100.7" {
		t.Errorf("clientIP() = %q, want the address the configured proxy reported", got)
	}
	// And loopback is no longer trusted, because the operator replaced the set
	// rather than adding to it. Saying so in a test because it is a real way to
	// break a working panel.
	if got := clientIP(req("127.0.0.1:41234", map[string]string{"X-Forwarded-For": "1.2.3.4"})); got != "127.0.0.1" {
		t.Errorf("clientIP() = %q; loopback was still trusted after being replaced", got)
	}
}

// A typo must not quietly empty the set. Trusting nothing would break a working
// deployment; trusting everything would be the vulnerability this exists to
// prevent.
func TestABadEntryIsReportedAndTheGoodOnesKept(t *testing.T) {
	restore(t)

	bad := SetTrustedProxies([]string{"10.0.0.0/8", "not-an-address"})
	if len(bad) != 1 || bad[0] != "not-an-address" {
		t.Errorf("SetTrustedProxies reported %v, want the one bad entry", bad)
	}
	if got := clientIP(req("10.0.0.5:5555", map[string]string{"X-Forwarded-For": "198.51.100.7"})); got != "198.51.100.7" {
		t.Errorf("clientIP() = %q; the good entry was dropped along with the bad one", got)
	}
}

// ── the scheme, which decides whether HSTS is sent ──────────────────────────

func TestTLSAtTheProxyCountsAsEncrypted(t *testing.T) {
	restore(t)

	got := clientScheme(req("127.0.0.1:41234", map[string]string{"X-Forwarded-Proto": "https"}))
	if got != "https" {
		t.Errorf("clientScheme() = %q; a panel served over HTTPS looked unencrypted", got)
	}
}

func TestAStrangerCannotClaimTheConnectionWasEncrypted(t *testing.T) {
	restore(t)

	got := clientScheme(req("203.0.113.9:5555", map[string]string{"X-Forwarded-Proto": "https"}))
	if got != "http" {
		t.Errorf("clientScheme() = %q; a stranger's claim of TLS was believed", got)
	}
}
