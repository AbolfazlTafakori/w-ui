package nodes

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// The rounds against a 3x-ui panel of each generation, end to end: its
// inbounds held as tunnels, a customer made a client there, counted,
// switched off and on, and taken away -- and its own operator's client never
// touched.

// xuiGenerations are the panels a 3x-ui node is, by how it is reached.
var xuiGenerations = []struct {
	name, version string
	token, login  bool
}{
	{"2.9 by password", "2.9.4", false, true},
	// 3.0 has its links only from its subscription, read signed in.
	{"3.0 by token and password", "3.0.2", true, true},
	{"3.10 by token", "3.10.2", true, false},
	// Signed in too, through its CSRF token.
	{"3.10 by token and password", "3.10.2", true, true},
}

func xuiNode(f *fakeXUI, token, login bool) model.Node {
	n := model.Node{Kind: model.KindXUI, Address: f.address(), Enabled: true}
	if token {
		n.Token = f.token
	}
	if login {
		n.Username, n.Password = f.user, f.pass
	}
	return n
}

func TestA3xuiCustomerFromStartToEnd(t *testing.T) {
	for _, g := range xuiGenerations {
		t.Run(g.name, func(t *testing.T) {
			f := newFakeXUI(t, g.version)
			in := f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
			owner := map[string]any{"id": "0b1c2d3e-0000-4000-8000-000000000001", "email": "owner-1", "enable": true, "subId": "ownersub"}
			f.addOwnClient(in.ID, owner)

			r := newRig(t, xuiNode(f, g.token, g.login))
			r.round()
			if got := r.tunnel("vless-in"); got.Protocol != model.ProtocolXray || !got.XUI.V.Customers || got.ListenPort != 31001 {
				t.Fatalf("tunnel held as %+v", got.XUI.V)
			}

			c := r.customer("alice", "vless-in")
			r.round()
			acc := r.account(c.ID, "vless-in")
			email := service.XUIEmail(r.tag(), &acc)
			got := f.client(in.ID, email)
			if got == nil {
				t.Fatalf("no client %s there; it has %v", email, f.emails(in.ID))
			}
			if got["id"] != acc.Secret || got["enable"] != true {
				t.Fatalf("client there is %v, want id %s on", got, acc.Secret)
			}
			// One user per file is its IP limit there.
			if got["limitIp"] != float64(1) {
				t.Errorf("IP limit %v, want 1", got["limitIp"])
			}

			// Its links are read back from there.
			r.round()
			if a := r.account(c.ID, "vless-in"); !strings.Contains(a.XUILink, acc.Secret) {
				t.Errorf("links %q do not carry its id", a.XUILink)
			}

			// What it moves there is charged here, once.
			f.count(in.ID, email, 1000, 4000)
			r.round()
			r.round()
			if got := r.chargedTo(c.ID); got != 5000 {
				t.Errorf("charged %d bytes, want 5000", got)
			}

			r.setStatus(c.ID, model.StatusDisabled)
			r.round()
			if f.client(in.ID, email)["enable"] != false {
				t.Fatal("a customer switched off here is still on there")
			}
			r.setStatus(c.ID, model.StatusActive)
			r.round()
			if f.client(in.ID, email)["enable"] != true {
				t.Fatal("a customer switched on again here is still off there")
			}

			if err := r.clients.Delete(r.ctx, c.ID); err != nil {
				t.Fatal(err)
			}
			r.round()
			if f.client(in.ID, email) != nil {
				t.Fatalf("a customer removed here is still there: %v", f.emails(in.ID))
			}

			// The operator's own client, through all of it, as it was.
			if have := f.client(in.ID, "owner-1"); have == nil || have["id"] != owner["id"] || have["enable"] != true {
				t.Fatalf("the operator's own client was changed: %v", have)
			}
			// One sign-in for every round, not one each.
			if g.login && f.logins > 1 {
				t.Errorf("signed in %d times; the sign-in is to be kept", f.logins)
			}
		})
	}
}

// 3x-ui 2 refuses to take the last client off an inbound; it is switched off
// and left instead, and the round goes on without an error.
func TestTheLastClientOnA3xui2InboundIsSwitchedOffNotRemoved(t *testing.T) {
	f := newFakeXUI(t, "2.9.4")
	in := f.addInbound("only", "vless", 31002, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, false, true))
	r.round()
	c := r.customer("bob", "only")
	r.round()
	acc := r.account(c.ID, "only")
	email := service.XUIEmail(r.tag(), &acc)
	if err := r.clients.Delete(r.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	r.round()
	got := f.client(in.ID, email)
	if got == nil || got["enable"] != false {
		t.Fatalf("the last client there is %v, want it kept and off", got)
	}
	r.round() // and the next round leaves it be
}

// A 3x-ui 3.10 that is given a password it refuses is still reached by its
// token: only what needs the sign-in is lost, not the node.
func TestA3xuiRefusingTheSignInIsStillReachedByItsToken(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	f.addInbound("vless-in", "vless", 31003, map[string]any{"decryption": "none"}, nil)
	n := xuiNode(f, true, true)
	n.Password = "not-it"
	r := newRig(t, n)
	r.round()
	r.tunnel("vless-in")
}

// A 3.0 given only a token cannot give a client's links; the round says
// what is missing rather than leaving the customer with nothing to dial.
func TestA3xui30ByTokenAloneSaysItNeedsThePassword(t *testing.T) {
	f := newFakeXUI(t, "3.0.2")
	f.addInbound("vless-in", "vless", 31004, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	r.customer("carol", "vless-in")
	err := r.roundErr()
	if err == nil || !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("round said %v", err)
	}
}

// The outbound at the top carries this panel's customers on the node, and
// theirs alone: put in its xray under this panel's tag, with a rule that
// matches the clients this panel made there; taken away when direct is at
// the top again.
func TestTheTopOutboundCarriesThisPanelsCustomersOnA3xuiNode(t *testing.T) {
	defer func(d time.Duration) { xuiSettle = d }(xuiSettle)
	xuiSettle = time.Millisecond
	f := newFakeXUI(t, "3.10.2")
	in := f.addInbound("vless-in", "vless", 31005, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	c := r.customer("dan", "vless-in")
	hop := model.Outbound{Tag: "de-hop", Kind: model.OutboundVLESS, Enabled: true, Position: -1,
		Config: `{"protocol":"vless","settings":{"vnext":[{"address":"de.example","port":443,"users":[{"id":"x"}]}]}}`}
	if err := r.db.Create(&hop).Error; err != nil {
		t.Fatal(err)
	}
	r.round()
	acc := r.account(c.ID, "vless-in")
	email := service.XUIEmail(r.tag(), &acc)
	if f.client(in.ID, email) == nil {
		t.Fatal("no client there")
	}
	cfg := f.xrayConfig()
	route := egressRoute(r.tag(), nil)
	var rule map[string]any
	for _, x := range cfg["routing"].(map[string]any)["rules"].([]any) {
		if m := x.(map[string]any); m["outboundTag"] == route.tag {
			rule = m
		}
	}
	if rule == nil {
		t.Fatalf("no rule for %s in %v", route.tag, cfg["routing"])
	}
	pattern := regexp.MustCompile(strings.TrimPrefix(rule["user"].([]any)[0].(string), "regexp:"))
	if !pattern.MatchString(email) || pattern.MatchString("owner-1") {
		t.Fatalf("the rule %v does not send %s, and only it, out", rule["user"], email)
	}
	restarts := f.restarts
	r.round()
	if f.restarts != restarts {
		t.Error("xray was restarted again with nothing changed")
	}

	// Direct at the top again: ours goes, and nothing else.
	if err := r.db.Delete(&hop).Error; err != nil {
		t.Fatal(err)
	}
	r.round()
	for _, o := range f.xrayConfig()["outbounds"].([]any) {
		if o.(map[string]any)["tag"] == route.tag {
			t.Fatal("the outbound is still there with direct at the top")
		}
	}
}

// An outbound its xray will not run with is taken out again, and the config
// put back as it was: the operator's own users keep their service.
func TestAnOutboundXrayWillNotRunWithIsPutBack(t *testing.T) {
	defer func(d time.Duration) { xuiSettle = d }(xuiSettle)
	xuiSettle = time.Millisecond
	f := newFakeXUI(t, "3.10.2")
	f.addInbound("vless-in", "vless", 31006, map[string]any{"decryption": "none"}, nil)
	before := f.xrayConfig()
	f.xrayFails = func(cfg map[string]any) bool { return len(cfg["outbounds"].([]any)) > 1 }
	r := newRig(t, xuiNode(f, true, false))
	if err := r.db.Create(&model.Outbound{Tag: "bad", Kind: model.OutboundVLESS, Enabled: true, Position: -1,
		Config: `{"protocol":"vless"}`}).Error; err != nil {
		t.Fatal(err)
	}
	err := r.roundErr()
	if err == nil || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("round said %v", err)
	}
	if !reflect.DeepEqual(f.xrayConfig(), before) {
		t.Fatalf("its config is %v, not as it was", f.xrayConfig())
	}
}

// An inbound made here is made there as that panel's own, with what its form
// asks; one its xray will not run with is taken out again, so its operator's
// users do not stop with it.
func TestAnInboundIsMadeOnA3xuiNodeOrTakenOutAgain(t *testing.T) {
	defer func(d time.Duration) { xuiSettle = d }(xuiSettle)
	xuiSettle = time.Millisecond
	f := newFakeXUI(t, "3.10.2")
	f.addInbound("theirs", "vless", 31201, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	node := r.node
	node.Version = "3x-ui 3.10.2"

	made, err := CreateXrayInbound(r.ctx, node, service.XrayInboundInput{
		Remark: "ours", Port: 31202, Protocol: "vless", Customers: true,
		Settings: map[string]any{"clients": []any{}, "decryption": "none"},
		Stream:   map[string]any{"network": "tcp", "security": "reality"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if made.RemoteID == 0 || made.Flow != "xtls-rprx-vision" || made.Network != "tcp" || made.Security != "reality" {
		t.Fatalf("made %+v", made)
	}
	if len(f.inbounds) != 2 || f.restarts == 0 {
		t.Fatalf("%d inbounds there, xray restarted %d times", len(f.inbounds), f.restarts)
	}

	f.inboundFails = func(ins []*fakeXUIInbound) bool {
		for _, in := range ins {
			if in.Protocol == "masque" {
				return true
			}
		}
		return false
	}
	_, err = CreateXrayInbound(r.ctx, node, service.XrayInboundInput{
		Remark: "broken", Port: 31203, Protocol: "masque", Customers: true,
		Settings: map[string]any{"clients": []any{}}, Stream: map[string]any{"network": "masque", "security": "tls"},
	})
	if err == nil || !strings.Contains(err.Error(), "taken out again") {
		t.Fatalf("made an inbound its xray will not run with: %v", err)
	}
	if len(f.inbounds) != 2 {
		t.Fatalf("%d inbounds there; the broken one is still there", len(f.inbounds))
	}
}
