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
)

// The whole configuration as one document -- the thing 3x-ui's advanced
// tab shows as the Xray template. Ours is read out of the database and
// written back into it, by name and tag, so an operator can keep it in a
// file, diff it, and paste it into another panel.

// TemplateDoc is the document.
type TemplateDoc struct {
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
	Routing   *TemplateRouting  `json:"routing,omitempty"`
	Balancers []BalancerInput   `json:"balancers"`
	DNS       *dnsproxy.Config  `json:"dns,omitempty"`
}

// TemplateRouting is the routing half: the basic page and the rule list.
type TemplateRouting struct {
	Basic *BasicRouting `json:"basic,omitempty"`
	Rules []RuleInput   `json:"rules"`
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
	outbounds  *Outbounds
	routing    *Routing
	balancers  *Balancers
	engine     *Engine
}

func NewTemplate(db *gorm.DB, ifaces *Interfaces, obs *Outbounds, routing *Routing, balancers *Balancers, engine *Engine, log *slog.Logger) *Template {
	return &Template{db: db, log: log, interfaces: ifaces, outbounds: obs, routing: routing, balancers: balancers, engine: engine}
}

// Export reads everything out.
func (t *Template) Export(ctx context.Context) (TemplateDoc, error) {
	doc := TemplateDoc{Inbounds: []json.RawMessage{}, Outbounds: []json.RawMessage{}, Balancers: []BalancerInput{}}

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
	tr := &TemplateRouting{Basic: &basic, Rules: []RuleInput{}}
	for _, r := range rules {
		en := r.Enabled
		tr.Rules = append(tr.Rules, RuleInput{
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
		doc.Balancers = append(doc.Balancers, BalancerInput{
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
	b, _ := json.Marshal(v)
	return b
}

// Apply writes a document in. Rows are matched by name or tag: a known one
// is updated, an unknown one created, and nothing is deleted -- deleting is
// done on the pages, where it asks. section narrows it to one part.
func (t *Template) Apply(ctx context.Context, doc TemplateDoc, section string) (TemplateResult, error) {
	var res TemplateResult
	skip := func(what string, err error) {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %v", what, err))
	}

	all := section == "" || section == "complete"

	if all || section == "inbounds" {
		existing, err := t.interfaces.List(ctx)
		if err != nil {
			return res, err
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
				skip("inbound", fmt.Errorf("every inbound needs a name"))
				continue
			}
			if cur, ok := byName[strings.ToLower(head.Name)]; ok {
				var in UpdateInterfaceInput
				if err := json.Unmarshal(raw, &in); err != nil {
					skip("inbound "+head.Name, err)
					continue
				}
				if _, err := t.interfaces.Update(ctx, cur.ID, in); err != nil {
					skip("inbound "+head.Name, err)
					continue
				}
				res.Updated++
				continue
			}
			var in CreateInterfaceInput
			if err := json.Unmarshal(raw, &in); err != nil {
				skip("inbound "+head.Name, err)
				continue
			}
			if _, err := t.interfaces.Create(ctx, in); err != nil {
				skip("inbound "+head.Name, err)
				continue
			}
			res.Created++
		}
	}

	if all || section == "outbounds" {
		existing, err := t.outbounds.List(ctx)
		if err != nil {
			return res, err
		}
		byTag := map[string]model.Outbound{}
		for _, o := range existing {
			byTag[strings.ToLower(o.Tag)] = o
		}
		for _, raw := range doc.Outbounds {
			var in OutboundInput
			if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Tag) == "" {
				skip("outbound", fmt.Errorf("every outbound needs a tag"))
				continue
			}
			if cur, ok := byTag[strings.ToLower(in.Tag)]; ok {
				if _, err := t.outbounds.Update(ctx, cur.ID, in); err != nil {
					skip("outbound "+in.Tag, err)
					continue
				}
				res.Updated++
				continue
			}
			if _, err := t.outbounds.Create(ctx, in); err != nil {
				skip("outbound "+in.Tag, err)
				continue
			}
			res.Created++
		}
	}

	if all || section == "routing" {
		// Balancers before rules, since rules may point at them.
		existing, err := t.balancers.List(ctx)
		if err != nil {
			return res, err
		}
		byTag := map[string]BalancerView{}
		for _, b := range existing {
			byTag[strings.ToLower(b.Tag)] = b
		}
		for _, in := range doc.Balancers {
			if strings.TrimSpace(in.Tag) == "" {
				skip("balancer", fmt.Errorf("every balancer needs a tag"))
				continue
			}
			if cur, ok := byTag[strings.ToLower(in.Tag)]; ok {
				if _, err := t.balancers.Update(ctx, cur.ID, in); err != nil {
					skip("balancer "+in.Tag, err)
					continue
				}
				res.Updated++
				continue
			}
			if _, err := t.balancers.Create(ctx, in); err != nil {
				skip("balancer "+in.Tag, err)
				continue
			}
			res.Created++
		}

		if doc.Routing != nil {
			if doc.Routing.Basic != nil {
				if _, err := t.routing.SaveBasic(ctx, *doc.Routing.Basic); err != nil {
					skip("basic routing", err)
				} else {
					res.Updated++
				}
			}
			rules, err := t.routing.ListRules(ctx)
			if err != nil {
				return res, err
			}
			byName := map[string]model.RoutingRule{}
			for _, r := range rules {
				byName[strings.ToLower(r.Name)] = r
			}
			var order []uint
			for _, in := range doc.Routing.Rules {
				if strings.TrimSpace(in.Name) == "" {
					skip("rule", fmt.Errorf("every rule needs a name"))
					continue
				}
				if cur, ok := byName[strings.ToLower(in.Name)]; ok {
					if _, err := t.routing.UpdateRule(ctx, cur.ID, in); err != nil {
						skip("rule "+in.Name, err)
						continue
					}
					order = append(order, cur.ID)
					res.Updated++
					continue
				}
				r, err := t.routing.CreateRule(ctx, in)
				if err != nil {
					skip("rule "+in.Name, err)
					continue
				}
				order = append(order, r.ID)
				res.Created++
			}
			// The document's order is the order.
			if len(order) > 0 {
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
					skip("rule order", err)
				}
			}
		}
	}

	if (all || section == "dns") && doc.DNS != nil {
		eng, err := t.engine.Get(ctx)
		if err != nil {
			return res, err
		}
		eng.DNS = *doc.DNS
		if _, err := t.engine.Save(ctx, eng); err != nil {
			skip("dns", err)
		} else {
			res.Updated++
		}
	}

	if res.Skipped == nil {
		res.Skipped = []string{}
	}
	return res, nil
}
