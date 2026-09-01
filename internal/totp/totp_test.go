package totp

import (
	"strings"
	"testing"
	"time"
)

// The vectors from RFC 6238 appendix B, for the SHA-1 twenty-byte secret
// "12345678901234567890". Passing these is what makes an authenticator app
// agree with us; a home-grown implementation that only agrees with itself is
// worse than none, because it locks the operator out of their own panel.
func TestMatchesTheRFCVectors(t *testing.T) {
	// "12345678901234567890" in base32, unpadded.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		got, err := Generate(secret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatalf("generate at %d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("code at %d = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestACodeIsAcceptedForAWhileEitherSide(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := Generate(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	// Phone clocks drift and people type slowly. Without a little tolerance a
	// correct code is refused often enough that the feature gets turned off.
	for _, offset := range []time.Duration{-Step, 0, Step} {
		if !Validate(secret, code, now.Add(offset)) {
			t.Errorf("a correct code was refused %s away", offset)
		}
	}
}

func TestAnOldCodeStopsWorking(t *testing.T) {
	secret, _ := NewSecret()
	now := time.Now()
	code, _ := Generate(secret, now)

	// A code that keeps working is a second password, not a second factor.
	if Validate(secret, code, now.Add(5*Step)) {
		t.Error("a code from two and a half minutes ago was still accepted")
	}
}

func TestWrongAndMalformedCodesAreRefused(t *testing.T) {
	secret, _ := NewSecret()
	now := time.Now()
	right, _ := Generate(secret, now)

	for _, entered := range []string{"", "12345", "1234567", "abcdef", "000000", right + "0"} {
		if entered == right {
			continue
		}
		if Validate(secret, entered, now) {
			t.Errorf("Validate accepted %q", entered)
		}
	}
}

func TestAnUnusableSecretNeverValidates(t *testing.T) {
	// Failing open here would mean a corrupt stored secret silently disabling
	// the second factor for the account that has it turned on.
	for _, secret := range []string{"", "not base32!", "===="} {
		if Validate(secret, "123456", time.Now()) {
			t.Errorf("Validate accepted a code against secret %q", secret)
		}
	}
}

func TestSecretsAreUniqueAndScannable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("the generator repeated %q", s)
		}
		seen[s] = true
		// Padding characters break QR readers and several authenticator apps.
		if strings.Contains(s, "=") {
			t.Errorf("secret %q contains padding", s)
		}
	}
}

func TestTheURIIsWhatAnAuthenticatorExpects(t *testing.T) {
	got := URI("W-UI", "admin", "GEZDGNBVGY3TQOJQ")

	for _, want := range []string{
		"otpauth://totp/",
		"secret=GEZDGNBVGY3TQOJQ",
		"issuer=W-UI",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("uri is missing %q:\n%s", want, got)
		}
	}
}
