package nodes

import (
	"strings"
	"testing"
)

// A file's links are read again the round its inbound changes there -- here
// its Reality name, which nothing else of what this panel holds of the
// inbound shows -- and not every few minutes for nothing: a node with
// thousands of customers would be asked for thousands of links all the time.
func TestA3xuiFilesLinksAreReadWhenItsInboundChangesAndNotOtherwise(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	in := f.addInbound("vless-in", "vless", 31501, map[string]any{"decryption": "none"},
		map[string]any{"network": "tcp", "security": "reality", "sni": "a.example"})
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	c := r.customer("hal", "vless-in")
	r.round()
	r.round()
	if !strings.Contains(r.account(c.ID, "vless-in").XUILink, "a.example") {
		t.Fatalf("links %q", r.account(c.ID, "vless-in").XUILink)
	}

	f.mu.Lock()
	reads := f.linkReads
	f.mu.Unlock()
	for i := 0; i < 5; i++ {
		r.round()
	}
	if f.linkReads != reads {
		t.Fatalf("links read %d times in five rounds with nothing changed", f.linkReads-reads)
	}

	f.mu.Lock()
	in.Stream["sni"] = "b.example"
	f.mu.Unlock()
	r.round()
	if got := r.account(c.ID, "vless-in").XUILink; !strings.Contains(got, "b.example") {
		t.Fatalf("its inbound's Reality name changed there, its links here did not: %q", got)
	}
	// A new customer there is no change of the inbound's.
	reads = f.linkReads
	r.customer("ida", "vless-in")
	r.round()
	r.round()
	if got := f.linkReads - reads; got != 1 {
		t.Fatalf("%d links read for one new customer; the others' are as they were", got)
	}
}

// On Pasarguard a host is as much an inbound's links as the inbound itself.
func TestAPasarguardFilesLinksAreReadWhenItsHostChanges(t *testing.T) {
	f := newFakePG(t)
	f.addInbound(vlessInbound("VLESS TCP", 32501))
	f.mu.Lock()
	f.hosts = append(f.hosts, map[string]any{"id": 1, "inbound_tag": "VLESS TCP", "address": []any{"a.example"}})
	f.mu.Unlock()
	r := newRig(t, pgNodeFor(f, true))
	r.round()
	c := r.customer("jo", "VLESS TCP")
	r.round()
	r.round()
	if !strings.Contains(r.account(c.ID, "VLESS TCP").XUILink, "a.example") {
		t.Fatalf("links %q", r.account(c.ID, "VLESS TCP").XUILink)
	}
	reads := f.linkReads
	r.round()
	r.round()
	if f.linkReads != reads {
		t.Fatal("links read again with nothing changed")
	}
	f.mu.Lock()
	f.hosts[0]["address"] = []any{"b.example"}
	f.mu.Unlock()
	r.round()
	if got := r.account(c.ID, "VLESS TCP").XUILink; !strings.Contains(got, "b.example") {
		t.Fatalf("its host changed there, its links here did not: %q", got)
	}
}

// The fingerprint is of the inbound, not of its clients.
func TestAnInboundsPrintIsNotItsClients(t *testing.T) {
	a := inboundPrint("vless", 443, "in", map[string]any{"clients": []any{1}, "decryption": "none"}, map[string]any{"network": "tcp"})
	b := inboundPrint("vless", 443, "in", map[string]any{"clients": []any{1, 2}, "decryption": "none"}, map[string]any{"network": "tcp"})
	c := inboundPrint("vless", 443, "in", map[string]any{"clients": []any{1}, "decryption": "none"}, map[string]any{"network": "ws"})
	if a != b || a == c {
		t.Fatalf("prints %s %s %s: the same with other clients, another with another transport", a, b, c)
	}
}
