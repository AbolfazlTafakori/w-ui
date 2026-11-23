package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/dnsproxy"
	"github.com/abolfazl/w-ui/internal/egress"
)

// templatePanel is a panel's configuration services on a test database,
// wired as the panel wires them.
type templatePanel struct {
	db  *gorm.DB
	tpl *Template
	rt  *egress.Routing
}

func newTemplatePanel(t *testing.T) templatePanel {
	t.Helper()
	// A file, not ":memory:": each connection to an in-memory database is
	// a database of its own, and creating a tunnel opens a second one.
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "w.db")+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	ifaces := newInterfaces(t, db)
	tuning := egress.NewTuning()
	rt := egress.NewRouting(db, tuning, quietLog())
	engine := NewEngine(db, rt, tuning, dnsproxy.New(quietLog()), quietLog())
	tpl := NewTemplate(db, ifaces, egress.NewOutbounds(db, tuning, quietLog()), rt, egress.NewBalancers(db, quietLog()), engine, quietLog())
	return templatePanel{db: db, tpl: tpl, rt: rt}
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// aDoc is a document with one of each kind of row.
func aDoc(t *testing.T, names func(string) string) TemplateDoc {
	t.Helper()
	on := true
	return TemplateDoc{
		Inbounds: []json.RawMessage{raw(t, map[string]any{"name": names("wg-a"), "protocol": "wireguard",
			"enabled": true, "listenPort": 51900, "subnet": "10.150.0.0/24", "endpointHost": "vpn.example.com"})},
		Outbounds: []json.RawMessage{
			raw(t, map[string]any{"tag": names("socks-a"), "kind": "socks", "enabled": true, "address": "198.51.100.7:1080"}),
			raw(t, map[string]any{"tag": names("socks-b"), "kind": "socks", "enabled": true, "address": "198.51.100.8:1080"}),
		},
		Balancers: []egress.BalancerInput{{Tag: names("bal-a"), Enabled: &on, Members: []string{names("socks-a"), names("socks-b")}}},
		Routing: &TemplateRouting{Rules: []egress.RuleInput{
			{Name: names("rule-a"), Enabled: &on, Domains: "example.com", OutboundTag: names("socks-a")},
			{Name: names("rule-b"), Enabled: &on, Domains: "example.org", OutboundTag: names("socks-b")},
		}},
	}
}

func count(t *testing.T, db *gorm.DB, m any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(m).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// A document applied to an empty panel makes every row it names; applied
// again, with its names in other letters' case, it matches each to the row
// it made and makes nothing twice.
func TestATemplateMakesItsRowsAndMatchesThemAfter(t *testing.T) {
	p := newTemplatePanel(t)
	ctx := context.Background()
	res, err := p.tpl.Apply(ctx, aDoc(t, func(s string) string { return s }), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 6 || res.Updated != 0 || len(res.Skipped) != 0 {
		t.Fatalf("first apply: %+v; want 6 made (1 tunnel, 2 outbounds, 1 balancer, 2 rules)", res)
	}
	tunnels, outbounds := count(t, p.db, &model.Interface{}), count(t, p.db, &model.Outbound{})

	res, err = p.tpl.Apply(ctx, aDoc(t, strings.ToUpper), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Updated != 6 || len(res.Skipped) != 0 {
		t.Fatalf("again, in upper case: %+v; want the 6 updated, none made", res)
	}
	if count(t, p.db, &model.Interface{}) != tunnels || count(t, p.db, &model.Outbound{}) != outbounds {
		t.Fatal("applying the document again made rows twice")
	}
	if res.Skipped == nil {
		t.Fatal("skipped is null in the answer, not an empty list")
	}
}

// A row with no name or tag is passed over, said in the answer, and the
// rest of the document is applied all the same.
func TestATemplateRowWithNoNameIsPassedOverAndTheRestApplied(t *testing.T) {
	p := newTemplatePanel(t)
	doc := aDoc(t, func(s string) string { return s })
	doc.Inbounds = append(doc.Inbounds, raw(t, map[string]any{"protocol": "wireguard"}))
	doc.Outbounds = append(doc.Outbounds, raw(t, map[string]any{"kind": "socks"}))
	doc.Balancers = append(doc.Balancers, egress.BalancerInput{})
	doc.Routing.Rules = append(doc.Routing.Rules, egress.RuleInput{Domains: "x.example"})
	res, err := p.tpl.Apply(context.Background(), doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 6 || len(res.Skipped) != 4 {
		t.Fatalf("%+v; want the 6 good rows made and the 4 nameless ones passed over", res)
	}
	for _, want := range []string{"inbound: every inbound needs a name", "outbound: every outbound needs a tag",
		"balancer: every balancer needs a tag", "rule: every rule needs a name"} {
		found := false
		for _, s := range res.Skipped {
			found = found || s == want
		}
		if !found {
			t.Errorf("skipped %q not said; got %q", want, res.Skipped)
		}
	}
}

// A section applies its own part of the document and leaves the rest.
func TestATemplateSectionAppliesItsPartAlone(t *testing.T) {
	p := newTemplatePanel(t)
	doc := aDoc(t, func(s string) string { return s })
	doc.DNS = &dnsproxy.Config{}
	res, err := p.tpl.Apply(context.Background(), doc, "outbounds")
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 2 || count(t, p.db, &model.Interface{}) != 0 || count(t, p.db, &model.RoutingRule{}) != 0 {
		t.Fatalf("section outbounds: %+v, tunnels %d, rules %d; want the 2 outbounds alone",
			res, count(t, p.db, &model.Interface{}), count(t, p.db, &model.RoutingRule{}))
	}
}

// The rules run in the document's order; a rule the panel has and the
// document does not name keeps its place after them.
func TestATemplatesRulesRunInItsOrder(t *testing.T) {
	p := newTemplatePanel(t)
	ctx := context.Background()
	doc := aDoc(t, func(s string) string { return s })
	if _, err := p.tpl.Apply(ctx, doc, ""); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := p.rt.CreateRule(ctx, egress.RuleInput{Name: "kept", Enabled: &on, Domains: "kept.example", OutboundTag: "socks-a"}); err != nil {
		t.Fatal(err)
	}
	doc.Routing.Rules = []egress.RuleInput{doc.Routing.Rules[1], doc.Routing.Rules[0]}
	if _, err := p.tpl.Apply(ctx, doc, "routing"); err != nil {
		t.Fatal(err)
	}
	rules, err := p.rt.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rules {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "rule-b,rule-a,kept" {
		t.Fatalf("rules run as %v; want the document's order, then the one it did not name", names)
	}
}

// The DNS part is applied when the document has one, and left alone when it
// has none.
func TestATemplatesDNSIsAppliedOnlyWhenGiven(t *testing.T) {
	p := newTemplatePanel(t)
	ctx := context.Background()
	before, err := p.tpl.engine.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.tpl.Apply(ctx, TemplateDoc{}, "dns")
	if err != nil || res.Updated != 0 {
		t.Fatalf("no DNS in the document: %+v %v; want nothing changed", res, err)
	}
	cfg := before.DNS
	cfg.Enabled = !cfg.Enabled
	res, err = p.tpl.Apply(ctx, TemplateDoc{DNS: &cfg}, "dns")
	if err != nil || res.Updated != 1 {
		t.Fatalf("a DNS part: %+v %v; want it applied", res, err)
	}
	after, err := p.tpl.engine.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.DNS.Enabled == before.DNS.Enabled {
		t.Fatal("the document's DNS part was not saved")
	}
}
