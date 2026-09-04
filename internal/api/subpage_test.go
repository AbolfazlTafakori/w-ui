package api

import (
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/service"
)

// The figures a customer reads off their own page. Getting these wrong is not
// a cosmetic fault: "how much do I have left" is the question this page exists
// to answer, and a wrong answer becomes a support message.
func TestSubPageArithmetic(t *testing.T) {
	cases := []struct {
		name        string
		quota, used uint64
		wantRemain  uint64
		wantPct     int
		wantUnlim   bool
	}{
		{"half spent", 1000, 500, 500, 50, false},
		{"nothing spent", 1000, 0, 1000, 0, false},
		{"exactly spent", 1000, 1000, 0, 100, false},
		// Usage can pass the ceiling by the bytes already in flight when the
		// kernel began dropping. A bar past 100% or a negative remainder would
		// both be nonsense to look at.
		{"over the ceiling", 1000, 1400, 0, 100, false},
		{"no ceiling at all", 0, 9999, 0, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := service.SubPage{QuotaBytes: tc.quota, UsedBytes: tc.used}
			if got := p.Remaining(); got != tc.wantRemain {
				t.Errorf("Remaining() = %d, want %d", got, tc.wantRemain)
			}
			if got := p.UsedPercent(); got != tc.wantPct {
				t.Errorf("UsedPercent() = %d, want %d", got, tc.wantPct)
			}
			if got := p.Unlimited(); got != tc.wantUnlim {
				t.Errorf("Unlimited() = %v, want %v", got, tc.wantUnlim)
			}
		})
	}
}

// A customer who has used their allowance is still "active" in the database
// until the reconciler's next sweep. The page must not tell them everything is
// fine while their connection is being dropped.
func TestSubStatusReadsTheAllowanceNotJustTheFlag(t *testing.T) {
	cases := []struct {
		status      string
		quota, used uint64
		wantText    string
		wantClass   string
	}{
		{"active", 1000, 100, "Active", "good"},
		{"active", 1000, 1000, "Data used up", "bad"},
		{"active", 0, 999999, "Active", "good"},
		{"disabled", 0, 0, "Switched off", "bad"},
		{"expired", 0, 0, "Expired", "bad"},
		{"exhausted", 0, 0, "Data used up", "bad"},
	}
	for _, tc := range cases {
		p := &service.SubPage{Status: tc.status, QuotaBytes: tc.quota, UsedBytes: tc.used}
		text, class := subStatus(p)
		if text != tc.wantText || class != tc.wantClass {
			t.Errorf("subStatus(%s, %d/%d) = %q/%q, want %q/%q",
				tc.status, tc.used, tc.quota, text, class, tc.wantText, tc.wantClass)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0: "0 B", 512: "512 B", 1024: "1.0 KiB",
		1536: "1.5 KiB", 1048576: "1.0 MiB", 5242880: "5.0 MiB",
		1073741824: "1.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanUntil(t *testing.T) {
	now := time.Now()
	// The unit is what is being tested, not the exact figure: the clock moves
	// between building the case and reading it, so pinning the number makes a
	// test that fails once in a while for no reason anybody can act on.
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(-time.Hour), "expired"},
		{now.Add(30 * time.Minute), "minutes left"},
		{now.Add(5 * time.Hour), "hours left"},
		{now.Add(72 * time.Hour), "days left"},
	}
	for _, tc := range cases {
		got := humanUntil(tc.at)
		if !strings.HasSuffix(got, tc.want) {
			t.Errorf("humanUntil(%v) = %q, want it to end in %q", tc.at, got, tc.want)
		}
	}
	// An hour and a day are the two boundaries where the unit changes.
	if got := humanUntil(now.Add(90 * time.Minute)); !strings.HasSuffix(got, "hours left") {
		t.Errorf("90 minutes = %q, want hours", got)
	}
	if got := humanUntil(now.Add(47 * time.Hour)); !strings.HasSuffix(got, "hours left") {
		t.Errorf("47 hours = %q, want hours — days only past two", got)
	}
}

// Every nonce must be different and must actually be random. A predictable one
// is worse than none, because injected markup could name it and the policy
// would then permit exactly the script it exists to block.
func TestNonceIsUniqueAndNotEmpty(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		n := newNonce()
		if n == "" {
			t.Fatal("an empty nonce would leave the page with no usable policy")
		}
		if seen[n] {
			t.Fatalf("nonce %q was produced twice", n)
		}
		seen[n] = true
	}
}

// The page is one HTML document with no reference to anything outside itself.
// A customer on a network that blocks a CDN, or with no route to one, still
// gets a page that works.
func TestSubPageLoadsNothingExternal(t *testing.T) {
	var buf strings.Builder
	err := subPageTemplate.Execute(&buf, subPageView{
		Page: &service.SubPage{
			Title: "W-UI", Name: "Ali", Status: "active",
			QuotaBytes: 1000, UsedBytes: 250, UpBytes: 50, DownBytes: 200,
			SubURL: "https://example.com/subscribe/tok",
		},
		Nonce: "abc", Dir: "ltr", HasQuota: true,
		StatusTxt: "Active", StatusCls: "good",
		Devices: []subPageDevice{{
			SubPageDevice: service.SubPageDevice{
				ID: 1, Name: "phone", Address: "10.0.0.2", Filename: "phone.conf",
				Config: "[Interface]",
			},
			QR: "data:image/png;base64,AAAA",
		}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()

	for _, bad := range []string{"http://cdn", "https://cdn", "//fonts.", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(html, bad) {
			t.Errorf("the page reaches out to %q", bad)
		}
	}
	// src= and href= may only be the data: QR, a query-only link, or the
	// customer's own subscription URL shown as text.
	for _, want := range []string{
		`src="data:image/png;base64,`,
		`href="?device=1"`,
		`nonce="abc"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected %q in the page", want)
		}
	}
}

// A device name is typed by an operator and a client name may be anything. The
// page must render them as text, not as markup.
func TestSubPageEscapesWhatPeopleTyped(t *testing.T) {
	var buf strings.Builder
	err := subPageTemplate.Execute(&buf, subPageView{
		Page: &service.SubPage{
			Title: `<script>alert(1)</script>`,
			Name:  `"><img src=x onerror=alert(1)>`,
		},
		Nonce: "n", Dir: "ltr",
		StatusTxt: "Active", StatusCls: "good",
		Devices: []subPageDevice{{
			SubPageDevice: service.SubPageDevice{
				ID: 1, Name: `<b>bold</b>`, Config: `</textarea><script>x</script>`,
			},
		}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := buf.String()

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a title containing markup was rendered as markup")
	}
	// The characters that would end an attribute or open a tag are the ones
	// that matter. What is left after they are entity-encoded is inert text,
	// even when it still reads like an event handler.
	if strings.Contains(html, `<img src=x`) {
		t.Error("an attribute injection in the client name produced a real tag")
	}
	if !strings.Contains(html, "&#34;&gt;&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("the client name was not entity-encoded as expected:\n%s", html)
	}
	if strings.Contains(html, "<b>bold</b>") {
		t.Error("a device name containing markup was rendered as markup")
	}
	// The one script tag on the page is ours, and it carries the nonce.
	if n := strings.Count(html, "<script"); n != 1 {
		t.Errorf("found %d script tags, want exactly the one we wrote", n)
	}
}

// An OpenVPN profile with an inline certificate chain is far past what a QR can
// carry. Encoding one produces something no phone resolves, so those get a
// download button instead and the page says nothing about a QR at all.
func TestQRLimitIsBelowATypicalOpenVPNProfile(t *testing.T) {
	if subPageQRLimit > 2000 {
		t.Errorf("the QR limit of %d is past what a phone camera reliably reads", subPageQRLimit)
	}
	// A WireGuard profile is comfortably under it; that is the case that must
	// keep its QR, because scanning is how the tunnel gets onto a phone.
	const wireguardProfile = 420
	if subPageQRLimit <= wireguardProfile {
		t.Errorf("the limit of %d would drop the QR from a WireGuard profile", subPageQRLimit)
	}
}
