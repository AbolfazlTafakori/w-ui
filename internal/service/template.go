package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/dnsproxy"
	"github.com/abolfazl/w-ui/internal/egress"
)

// The whole configuration as one document -- the thing the classic panel's advanced
// tab shows as the Xray template. Ours is read out of the database and
// written back into it, by name and tag, so an operator can keep it in a
// file, diff it, and paste it into another panel.

// TemplateDoc is the document.
type TemplateDoc struct {
	Inbounds  []json.RawMessage      `json:"inbounds"`
	Outbounds []json.RawMessage      `json:"outbounds"`
	Routing   *TemplateRouting       `json:"routing,omitempty"`
	Balancers []egress.BalancerInput `json:"balancers"`
	DNS       *dnsproxy.Config       `json:"dns,omitempty"`
}

// TemplateRouting is the routing half: the basic page and the rule list.
type TemplateRouting struct {
	Basic *egress.BasicRouting `json:"basic,omitempty"`
	Rules []egress.RuleInput   `json:"rules"`
}

// TemplateResult says what applying did.
type TemplateResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped []string `json:"skipped"`
}

// Template exports and applies the document.
type Template struct {
	db         *gorm.DB
	log        *slog.Logger
	interfaces *Interfaces
	outbounds  *egress.Outbounds
	routing    *egress.Routing
	balancers  *egress.Balancers
	engine     *Engine
}

func NewTemplate(db *gorm.DB, ifaces *Interfaces, obs *egress.Outbounds, routing *egress.Routing, balancers *egress.Balancers, engine *Engine, log *slog.Logger) *Template {
	return &Template{db: db, log: log, interfaces: ifaces, outbounds: obs, routing: routing, balancers: balancers, engine: engine}
}

// Export reads everything out.
func (t *Template) Export(ctx context.Context) (TemplateDoc, error) {
	doc := TemplateDoc{Inbounds: []json.RawMessage{}, Outbounds: []json.RawMessage{}, Balancers: []egress.BalancerInput{}}

	ifaces, err := t.interfaces.List(ctx)
	if err != nil {
		return doc, err
	}
	for _, i := range ifaces {
		row := map[string]any{
			"name": i.Name, "protocol": i.Protocol, "enabled": i.Enabled, "listenPort": i.ListenPort,
			"subnet": i.Subnet, "endpointHost": i.EndpointHost, "mtu": i.MTU, "dns": i.DNS,
			"natInterface": i.NATInterface, "mode": i.Mode, "nodeId": i.NodeID,
		}
		if i.Protocol == model.ProtocolOpenVPN && i.OpenVPN.V.Transport != "" {
			row["transport"] = i.OpenVPN.V.Transport
		}
		// The key every customer's device holds. A panel rebuilt from this
		// document with a new one would have every device ask for it again.
		if i.Protocol == model.ProtocolL2TP && i.L2TP.V.PSK != "" {
			row["psk"] = i.L2TP.V.PSK
		}
		if i.Protocol == model.ProtocolL2TP && i.L2TP.V.AllowPlain {
			row["l2tpPlain"] = true
		}
		doc.Inbounds = append(doc.Inbounds, mustJSON(row))
	}

	obs, err := t.outbounds.List(ctx)
	if err != nil {
		return doc, err
	}
	for _, o := range obs {
		row := map[string]any{
			"tag": o.Tag, "kind": o.Kind, "enabled": o.Enabled, "address": o.Address, "note": o.Note,
		}
		if o.Kind.NeedsHop() {
			row["hopAddress"] = o.HopAddress
			row["hopDns"] = o.HopDNS
			row["hopMtu"] = o.HopMTU
			row["allowedIps"] = o.AllowedIPs
			row["keepalive"] = o.Keepalive
			row["peerPubKey"] = o.PeerPubKey
			// Secrets travel with the document on purpose: the point of it is
			// to rebuild the same panel elsewhere.
			row["privateKey"] = o.PrivateKey
			row["presharedKey"] = o.PresharedKey
			row["config"] = o.Config
		}
		if o.Username != "" || o.Password != "" {
			row["username"] = o.Username
			row["password"] = o.Password
		}
		doc.Outbounds = append(doc.Outbounds, mustJSON(row))
	}

	basic, err := t.routing.Basic(ctx)
	if err != nil {
		return doc, err
	}
	rules, err := t.routing.ListRules(ctx)
	if err != nil {
		return doc, err
	}
	tr := &TemplateRouting{Basic: &basic, Rules: []egress.RuleInput{}}
	for _, r := range rules {
		en := r.Enabled
		tr.Rules = append(tr.Rules, egress.RuleInput{
			Name: r.Name, Enabled: &en, SourceIPs: r.SourceIPs, SourcePorts: r.SourcePorts,
			Network: r.Network, DestIPs: r.DestIPs, Domains: r.Domains, Ports: r.Ports,
			Clients: r.Clients, Groups: r.Groups, Interfaces: r.Interfaces,
			OutboundTag: r.OutboundTag, Note: r.Note,
		})
	}
	doc.Routing = tr

	bals, err := t.balancers.List(ctx)
	if err != nil {
		return doc, err
	}
	for _, b := range bals {
		en := b.Enabled
		doc.Balancers = append(doc.Balancers, egress.BalancerInput{
			Tag: b.Tag, Enabled: &en, Strategy: b.Strategy, Members: b.MemberList, Note: b.Note, Fallback: b.Fallback,
		})
	}

	eng, err := t.engine.Get(ctx)
	if err != nil {
		return doc, err
	}
	dns := eng.DNS
	doc.DNS = &dns
	return doc, nil
}

func mustJSON(v any) json.RawMessage {
	// Marshalling plain data the panel built (maps, strings, numbers): it cannot fail.
	b, _ := json.Marshal(v)
	return b
}

// Apply writes a document in. Rows are matched by name or tag: a known one
// is updated, an unknown one created, and nothing is deleted -- deleting is
// done on the pages, where it asks. section narrows it to one part.
//
// A row that cannot be applied is passed over and said in Skipped; what
// fails Apply as a whole is the panel not being able to read what it has.
func (t *Template) Apply(ctx context.Context, doc TemplateDoc, section string) (TemplateResult, error) {
	res := TemplateResult{Skipped: []string{}}
	all := section == "" || section == "complete"
	steps := []struct {
		on  bool
		run func(context.Context, TemplateDoc, *TemplateResult) error
	}{
		{all || section == "inbounds", t.applyInbounds},
		{all || section == "outbounds", t.applyOutbounds},
		// Balancers before rules, since rules may point at them.
		{all || section == "routing", t.applyBalancers},
		{all || section == "routing", t.applyRouting},
		{(all || section == "dns") && doc.DNS != nil, t.applyDNS},
	}
	for _, step := range steps {
		if !step.on {
			continue
		}
		if err := step.run(ctx, doc, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// skip notes a row passed over.
func (r *TemplateResult) skip(what string, err error) {
	r.Skipped = append(r.Skipped, fmt.Sprintf("%s: %v", what, err))
}

// applyInbounds updates the tunnels the document names that the panel has,
// by name, and makes the others.
func (t *Template) applyInbounds(ctx context.Context, doc TemplateDoc, res *TemplateResult) error {
	existing, err := t.interfaces.List(ctx)
	if err != nil {
		return err
	}
	byName := map[string]model.Interface{}
	for _, i := range existing {
		byName[strings.ToLower(i.Name)] = i
	}
	for _, raw := range doc.Inbounds {
		var head struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &head); err != nil || strings.TrimSpace(head.Name) == "" {
			res.skip("inbound", fmt.Errorf("every inbound needs a name"))
			continue
		}
		if cur, ok := byName[strings.ToLower(head.Name)]; ok {
			var in UpdateInterfaceInput
			if err := json.Unmarshal(raw, &in); err != nil {
				res.skip("interface "+head.Name, err)
				continue
			}
			if _, err := t.interfaces.Update(ctx, cur.ID, in); err != nil {
				res.skip("interface "+head.Name, err)
				continue
			}
			res.Updated++
			continue
		}
		var in CreateInterfaceInput
		if err := json.Unmarshal(raw, &in); err != nil {
			res.skip("interface "+head.Name, err)
			continue
		}
		if _, err := t.interfaces.Create(ctx, in); err != nil {
			res.skip("interface "+head.Name, err)
			continue
		}
		res.Created++
	}
	return nil
}

// applyOutbounds updates the outbounds the document names that the panel
// has, by tag, and makes the others.
func (t *Template) applyOutbounds(ctx context.Context, doc TemplateDoc, res *TemplateResult) error {
	existing, err := t.outbounds.List(ctx)
	if err != nil {
		return err
	}
	byTag := map[string]model.Outbound{}
	for _, o := range existing {
		byTag[strings.ToLower(o.Tag)] = o
	}
	for _, raw := range doc.Outbounds {
		var in egress.OutboundInput
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Tag) == "" {
			res.skip("outbound", fmt.Errorf("every outbound needs a tag"))
			continue
		}
		if cur, ok := byTag[strings.ToLower(in.Tag)]; ok {
			if _, err := t.outbounds.Update(ctx, cur.ID, in); err != nil {
				res.skip("outbound "+in.Tag, err)
				continue
			}
			res.Updated++
			continue
		}
		if _, err := t.outbounds.Create(ctx, in); err != nil {
			res.skip("outbound "+in.Tag, err)
			continue
		}
		res.Created++
	}
	return nil
}

// applyBalancers updates the balancers the document names that the panel
// has, by tag, and makes the others.
func (t *Template) applyBalancers(ctx context.Context, doc TemplateDoc, res *TemplateResult) error {
	existing, err := t.balancers.List(ctx)
	if err != nil {
		return err
	}
	byTag := map[string]egress.BalancerView{}
	for _, b := range existing {
		byTag[strings.ToLower(b.Tag)] = b
	}
	for _, in := range doc.Balancers {
		if strings.TrimSpace(in.Tag) == "" {
			res.skip("balancer", fmt.Errorf("every balancer needs a tag"))
			continue
		}
		if cur, ok := byTag[strings.ToLower(in.Tag)]; ok {
			if _, err := t.balancers.Update(ctx, cur.ID, in); err != nil {
				res.skip("balancer "+in.Tag, err)
				continue
			}
			res.Updated++
			continue
		}
		if _, err := t.balancers.Create(ctx, in); err != nil {
			res.skip("balancer "+in.Tag, err)
			continue
		}
		res.Created++
	}
	return nil
}

// applyRouting saves the document's routing page and its rules, the rules
// by name, and puts them in the document's order, the panel's other rules
// after them.
func (t *Template) applyRouting(ctx context.Context, doc TemplateDoc, res *TemplateResult) error {
	if doc.Routing == nil {
		return nil
	}
	if doc.Routing.Basic != nil {
		if _, err := t.routing.SaveBasic(ctx, *doc.Routing.Basic); err != nil {
			res.skip("basic routing", err)
		} else {
			res.Updated++
		}
	}
	rules, err := t.routing.ListRules(ctx)
	if err != nil {
		return err
	}
	byName := map[string]model.RoutingRule{}
	for _, r := range rules {
		byName[strings.ToLower(r.Name)] = r
	}
	var order []uint
	for _, in := range doc.Routing.Rules {
		if strings.TrimSpace(in.Name) == "" {
			res.skip("rule", fmt.Errorf("every rule needs a name"))
			continue
		}
		if cur, ok := byName[strings.ToLower(in.Name)]; ok {
			if _, err := t.routing.UpdateRule(ctx, cur.ID, in); err != nil {
				res.skip("rule "+in.Name, err)
				continue
			}
			order = append(order, cur.ID)
			res.Updated++
			continue
		}
		r, err := t.routing.CreateRule(ctx, in)
		if err != nil {
			res.skip("rule "+in.Name, err)
			continue
		}
		order = append(order, r.ID)
		res.Created++
	}
	if len(order) == 0 {
		return nil
	}
	// The document's order is the order; the panel's other rules follow.
	seen := map[uint]bool{}
	for _, id := range order {
		seen[id] = true
	}
	for _, r := range rules {
		if !seen[r.ID] {
			order = append(order, r.ID)
		}
	}
	if err := t.routing.ReorderRules(ctx, order); err != nil {
		res.skip("rule order", err)
	}
	return nil
}

// applyDNS saves the document's DNS part.
func (t *Template) applyDNS(ctx context.Context, doc TemplateDoc, res *TemplateResult) error {
	eng, err := t.engine.Get(ctx)
	if err != nil {
		return err
	}
	eng.DNS = *doc.DNS
	if _, err := t.engine.Save(ctx, eng); err != nil {
		res.skip("dns", err)
	} else {
		res.Updated++
	}
	return nil
}
