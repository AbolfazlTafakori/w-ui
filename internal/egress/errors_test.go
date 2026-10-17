package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Every message an operator can be shown for bad input is exact and names
// the field it belongs to; these pin the outbound side's.

type wantErr struct {
	field string
	msg   string
}

func check(t *testing.T, name string, err error, want wantErr) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, wanted %q", name, want.msg)
		return
	}
	if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) {
		t.Errorf("%s: %v is neither invalid nor not-found", name, err)
	}
	if got := fault.FieldOf(err); want.field != "" && got != want.field {
		t.Errorf("%s: field %q, want %q (%v)", name, got, want.field, err)
	}
	if !strings.Contains(err.Error(), want.msg) {
		t.Errorf("%s: got %q, want it to contain %q", name, err.Error(), want.msg)
	}
}

func TestOutboundAndBalancerInputErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	outs := NewOutbounds(db, nil, quietLog())
	bals := NewBalancers(db, quietLog())

	ocases := []struct {
		name string
		in   OutboundInput
		want wantErr
	}{
		{"no tag", OutboundInput{Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", "an outbound needs a tag; routing rules refer to it by that name"}},
		{"bad tag", OutboundInput{Tag: "bad tag!", Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", "a tag can only contain letters, digits, - and _"}},
		{"built-in tag", OutboundInput{Tag: "direct", Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", `"direct" is the name of a built-in outbound`}},
		{"unknown kind", OutboundInput{Tag: "x", Kind: "carrier-pigeon", Address: "1.2.3.4:1080"}, wantErr{"kind", `"carrier-pigeon" is not an outbound kind this panel serves`}},
		{"no address", OutboundInput{Tag: "x", Kind: "socks"}, wantErr{"address", "an outbound of this kind needs an address to reach"}},
		{"wireguard without peer", OutboundInput{Tag: "x", Kind: "wireguard", Address: "1.2.3.4:51820", PrivateKey: strings.Repeat("A", 43) + "="}, wantErr{"peerPubKey", "a WireGuard hop needs the upstream peer's public key"}},
	}
	for _, c := range ocases {
		_, err := outs.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}

	bcases := []struct {
		name string
		in   BalancerInput
		want wantErr
	}{
		{"no tag", BalancerInput{Members: []string{"a"}}, wantErr{"tag", "a balancer needs a tag; rules refer to it by that name"}},
		{"no members", BalancerInput{Tag: "b"}, wantErr{"members", "a balancer needs at least one outbound to send traffic to"}},
		{"bad strategy", BalancerInput{Tag: "b", Members: []string{"a"}, Strategy: "roundrobin"}, wantErr{"strategy", `"roundrobin" is not a strategy; use random or leastPing`}},
		{"unknown member", BalancerInput{Tag: "b", Members: []string{"nope"}}, wantErr{"members", `there is no outbound called "nope"`}},
	}
	for _, c := range bcases {
		_, err := bals.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestRoutingRuleInputErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	routing := NewRouting(db, nil, quietLog())
	cases := []struct {
		name string
		in   RuleInput
		want wantErr
	}{
		{"no comment", RuleInput{DestIPs: "1.1.1.1", OutboundTag: "direct"}, wantErr{"name", "give the rule a comment so the list can be read later"}},
		{"matches nothing", RuleInput{Name: "r", OutboundTag: "direct"}, wantErr{"destIps", "the rule matches nothing as written; fill in at least one criterion"}},
		{"bad network", RuleInput{Name: "r", DestIPs: "1.1.1.1", Network: "sctp", OutboundTag: "direct"}, wantErr{"network", `"sctp" is not a network the router matches; use tcp, udp or icmp`}},
		{"icmp with ports", RuleInput{Name: "r", DestIPs: "1.1.1.1", Network: "icmp", Ports: "53", OutboundTag: "direct"}, wantErr{"network", "icmp has no ports; drop the ports or pick tcp or udp"}},
		{"unknown outbound", RuleInput{Name: "r", DestIPs: "1.1.1.1", OutboundTag: "nowhere"}, wantErr{"outboundTag", `there is no outbound or balancer called "nowhere"`}},
		{"unknown client", RuleInput{Name: "r", Clients: "abc", OutboundTag: "direct"}, wantErr{"clients", `a client is named by id here, and "abc" is not one`}},
	}
	for _, c := range cases {
		_, err := routing.CreateRule(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}
