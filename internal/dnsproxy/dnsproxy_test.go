package dnsproxy

import (
	"log/slog"
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0x1234, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func answers(t *testing.T, msg []byte) (dnsmessage.Header, []netip.Addr) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.SkipAllQuestions()
	var out []netip.Addr
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			break
		}
		switch rh.Type {
		case dnsmessage.TypeA:
			r, _ := p.AResource()
			out = append(out, netip.AddrFrom4(r.A))
		case dnsmessage.TypeAAAA:
			r, _ := p.AAAAResource()
			out = append(out, netip.AddrFrom16(r.AAAA))
		default:
			_ = p.SkipAnswer()
		}
	}
	return h, out
}

func TestHostsArePinned(t *testing.T) {
	p := New(slog.Default())
	p.Reconfigure(Config{
		Enabled: true,
		Hosts:   []Host{{Domain: "*.example.com", Values: []string{"10.0.0.5", "fd00::5"}}},
		Servers: []Server{{Address: "192.0.2.1"}}, // never reached
	}, nil)

	h, got := answers(t, p.handle(query(t, "www.example.com.", dnsmessage.TypeA), false))
	if h.ID != 0x1234 || !h.Response || h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("header = %+v", h)
	}
	if len(got) != 1 || got[0] != netip.MustParseAddr("10.0.0.5") {
		t.Fatalf("A answers = %v", got)
	}
	_, got6 := answers(t, p.handle(query(t, "www.example.com.", dnsmessage.TypeAAAA), false))
	if len(got6) != 1 || got6[0] != netip.MustParseAddr("fd00::5") {
		t.Fatalf("AAAA answers = %v", got6)
	}
}

func TestStrategyRefusesTheOtherFamily(t *testing.T) {
	p := New(slog.Default())
	p.Reconfigure(Config{
		Enabled:       true,
		QueryStrategy: "UseIPv4",
		Hosts:         []Host{{Domain: "a.test", Values: []string{"fd00::1"}}},
		Servers:       []Server{{Address: "192.0.2.1"}},
	}, nil)
	h, got := answers(t, p.handle(query(t, "a.test.", dnsmessage.TypeAAAA), false))
	if h.RCode != dnsmessage.RCodeSuccess || len(got) != 0 {
		t.Fatalf("expected an empty success, got rcode=%v answers=%v", h.RCode, got)
	}
}

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		name, rule string
		want       bool
	}{
		{"www.example.com", "example.com", true},
		{"example.com", "example.com", true},
		{"notexample.com", "example.com", false},
		{"a.b.c", "domain:b.c", true},
		{"x.y", "full:x.y", true},
		{"z.x.y", "full:x.y", false},
		{"google.com", "keyword:goog", true},
		{"a", "regexp:.*", false},
	}
	for _, c := range cases {
		if got := matchDomain(c.name, c.rule); got != c.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", c.name, c.rule, got, c.want)
		}
	}
}
