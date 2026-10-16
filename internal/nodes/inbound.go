package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Making an inbound on a 3x-ui or Pasarguard node from this panel's form.
//
// The form gathers what that panel's own form does -- the protocol's
// settings, the transport and security, sniffing, and on 3x-ui the
// inbound's allowance and expiry, on Pasarguard its host -- and the inbound
// is made there as that panel's own, with no clients: this panel's customers
// are added to it after, as to any inbound read from there.

// CreateXrayInbound makes the inbound on the node and says what it is.
func CreateXrayInbound(ctx context.Context, node model.Node, in service.XrayInboundInput) (model.XUIInbound, error) {
	out := model.XUIInbound{Remark: in.Remark, Kind: in.Protocol, Enabled: true}
	if st := in.Stream; st != nil {
		out.Network, _ = st["network"].(string)
		out.Security, _ = st["security"].(string)
		if out.Network == "raw" {
			out.Network = "tcp"
		}
	}
	if m, ok := in.Settings["method"].(string); ok {
		out.Method = m
	}
	if in.Protocol == "vless" && out.Network == "tcp" && (out.Security == "reality" || out.Security == "tls") {
		out.Flow = "xtls-rprx-vision"
	}
	switch node.Kind {
	case model.KindXUI:
		api, err := newXUIAPI(ctx, node)
		if err != nil {
			return out, err
		}
		id, tag, err := api.addInbound(ctx, in)
		if err != nil {
			return out, err
		}
		// That panel's xray must run with it: if not, every inbound there
		// stops, its operator's own users' with them. Taken out again, and
		// the reason given.
		// Restarted now rather than when that panel gets round to it, so
		// what runs is what was just made.
		_ = api.restartXray(ctx)
		if err := api.xrayRunsAfter(ctx); err != nil {
			_ = api.do(ctx, http.MethodPost, fmt.Sprintf("inbounds/del/%d", id), nil, nil)
			_ = api.restartXray(ctx)
			return out, fmt.Errorf("its xray did not run with this inbound, so it was taken out again: %w", err)
		}
		out.RemoteID, out.Tag = id, tag
		return out, nil
	case model.KindPG:
		api, err := newPGAPI(ctx, node)
		if err != nil {
			return out, err
		}
		if err := api.addInbound(ctx, in, xuiHost(node.Address)); err != nil {
			return out, err
		}
		out.RemoteID, out.Tag = pgRemoteID(in.Remark), in.Remark
		return out, nil
	}
	return out, errors.New("inbounds are made here only on 3x-ui and Pasarguard nodes")
}

// xrayRunsAfter waits a few seconds for the panel's xray to come up with a
// change, and says why it did not.
func (x *xuiAPI) xrayRunsAfter(ctx context.Context) error {
	var last xuiStatus
	for i := 0; i < 8; i++ {
		time.Sleep(time.Second)
		st, err := x.status(ctx)
		if err == nil && (st.Xray.State == "running" || st.Xray.State == "") {
			if i >= 2 {
				return nil
			}
			continue
		}
		last = st
	}
	msg := last.Xray.Error
	if msg == "" {
		msg = last.Xray.State
	}
	return fmt.Errorf("%s", msg)
}

// nodesRunAfter waits for a Pasarguard panel's nodes to come back connected.
func (p *pgAPI) nodesRunAfter(ctx context.Context) error {
	var last []pgNode
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		ns, err := p.nodes(ctx)
		if err != nil {
			continue
		}
		last = ns
		up := len(ns) > 0
		for _, n := range ns {
			if n.Status != "connected" {
				up = false
			}
		}
		if up && i >= 1 {
			return nil
		}
	}
	for _, n := range last {
		if n.Status != "connected" {
			return fmt.Errorf("%s is %s: %s", n.Name, n.Status, n.Message)
		}
	}
	return errors.New("no node of its own answered")
}

// addInbound makes an inbound on a 3x-ui panel; its id and tag there.
func (x *xuiAPI) addInbound(ctx context.Context, in service.XrayInboundInput) (int, string, error) {
	enc := func(v any) string {
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	sniffing := in.Sniffing
	if sniffing == nil {
		sniffing = map[string]any{"enabled": false}
	}
	body := map[string]any{
		"remark": in.Remark, "enable": true, "listen": in.Listen, "port": in.Port, "protocol": in.Protocol,
		"settings": enc(in.Settings), "streamSettings": enc(in.Stream), "sniffing": enc(sniffing),
		// The inbound's own allowance and expiry, as 3x-ui keeps them.
		"total": in.Total, "expiryTime": in.ExpiryTime,
	}
	if in.TrafficReset != "" {
		body["trafficReset"] = in.TrafficReset
		day := in.TrafficResetDay
		if day == 0 {
			day = 1
		}
		body["trafficResetDay"] = day
	}
	var made struct {
		ID  int    `json:"id"`
		Tag string `json:"tag"`
	}
	if err := x.do(ctx, http.MethodPost, "inbounds/add", body, &made); err != nil {
		return 0, "", fmt.Errorf("the 3x-ui panel refused the inbound: %w", err)
	}
	if made.Tag == "" {
		made.Tag = fmt.Sprintf("inbound-%d", in.Port)
	}
	return made.ID, made.Tag, nil
}

// addInbound puts an inbound in a Pasarguard panel's first core, restarts its
// nodes with it, and gives it a host so its users have links.
func (p *pgAPI) addInbound(ctx context.Context, in service.XrayInboundInput, nodeHost string) error {
	var cores struct {
		Cores []struct {
			ID     int            `json:"id"`
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		} `json:"cores"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/cores", nil, &cores); err != nil {
		return err
	}
	if len(cores.Cores) == 0 {
		return errors.New("the Pasarguard panel has no core to put the inbound in")
	}
	core := cores.Cores[0]
	before, _ := json.Marshal(core.Config) // to put back if its node will not run with it
	list, _ := core.Config["inbounds"].([]any)
	for _, o := range list {
		m, _ := o.(map[string]any)
		if m["tag"] == in.Remark {
			return fmt.Errorf("an inbound tagged %q is already on that Pasarguard panel", in.Remark)
		}
		if fmt.Sprint(m["port"]) == fmt.Sprint(in.Port) {
			return fmt.Errorf("port %d is already an inbound's on that Pasarguard panel", in.Port)
		}
	}
	listen := in.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	inbound := map[string]any{"tag": in.Remark, "listen": listen, "port": in.Port, "protocol": in.Protocol, "settings": in.Settings}
	if in.Stream != nil {
		inbound["streamSettings"] = in.Stream
	}
	if in.Sniffing != nil {
		inbound["sniffing"] = in.Sniffing
	}
	core.Config["inbounds"] = append(list, inbound)
	name := core.Name
	if name == "" {
		name = "core"
	}
	if err := p.do(ctx, http.MethodPut, fmt.Sprintf("/api/core/%d?restart_nodes=true", core.ID), map[string]any{
		"name": name, "config": core.Config, "exclude_inbound_tags": []string{}, "fallbacks_inbound_tags": []string{},
	}, nil); err != nil {
		return fmt.Errorf("the Pasarguard panel refused the inbound: %w", err)
	}
	// Its nodes restart with the core: one that does not come back would
	// carry none of that panel's users, its operator's own included.
	if err := p.nodesRunAfter(ctx); err != nil {
		var old map[string]any
		_ = json.Unmarshal(before, &old)
		_ = p.do(ctx, http.MethodPut, fmt.Sprintf("/api/core/%d?restart_nodes=true", core.ID), map[string]any{
			"name": name, "config": old, "exclude_inbound_tags": []string{}, "fallbacks_inbound_tags": []string{},
		}, nil)
		return fmt.Errorf("its node did not run with this inbound, so the core was put back as it was: %w", err)
	}
	// Only an inbound with users has a host: Pasarguard writes links from it.
	if !in.Customers {
		return nil
	}
	h := map[string]any{"remark": "{USERNAME} " + in.Remark, "address": []string{nodeHost}, "port": in.Port, "inbound_tag": in.Remark, "priority": 0}
	if hi := in.Host; hi != nil {
		if len(hi.Address) > 0 {
			h["address"] = hi.Address
		}
		if hi.Port > 0 {
			h["port"] = hi.Port
		}
		if len(hi.SNI) > 0 {
			h["sni"] = hi.SNI
		}
		if len(hi.Host) > 0 {
			h["host"] = hi.Host
		}
		if hi.Path != "" {
			h["path"] = hi.Path
		}
		if hi.Fingerprint != "" {
			h["fingerprint"] = hi.Fingerprint
		}
		if len(hi.ALPN) > 0 {
			h["alpn"] = hi.ALPN
		}
	}
	if err := p.do(ctx, http.MethodPost, "/api/host/", h, nil); err != nil {
		return fmt.Errorf("the inbound is made, but its host could not be: %w", err)
	}
	if m, _ := in.Settings["method"].(string); in.Protocol == "shadowsocks" && strings.HasPrefix(m, "2022-") {
		if err := p.keepMultiUser(ctx, core.ID, name, in.Remark); err != nil {
			return fmt.Errorf("the inbound is made, but its standing user could not be: %w", err)
		}
	}
	return nil
}

// keepMultiUser gives a Shadowsocks-2022 inbound a user of its own for good.
// A Pasarguard node starts such an inbound with the users it has then, and
// with none xray runs it as a one-key server that takes no users: the first
// one added afterwards fails, and with it that panel's sync of every user on
// the node, its operator's own included. This user, whose key nobody is
// given, keeps the inbound one with users through every start; the node is
// restarted once so it starts so now.
func (p *pgAPI) keepMultiUser(ctx context.Context, coreID int, coreName, tag string) error {
	gs, err := p.groups(ctx)
	if err != nil {
		return err
	}
	gid := 0
	for _, g := range gs {
		if len(g.InboundTags) == 1 && g.InboundTags[0] == tag && g.Name == pgGroupName(tag) {
			gid = g.ID
		}
	}
	if gid == 0 {
		g, err := p.createGroup(ctx, pgGroupName(tag), tag)
		if err != nil {
			return err
		}
		gid = g.ID
	}
	// Not a name of this panel's customers' (wui_<tag>_<number>), so no
	// round takes it for one and removes it.
	name := fmt.Sprintf("wuikeep_%08x", crc32.ChecksumIEEE([]byte(tag)))
	if _, err := p.createUser(ctx, name, "active", "Keeps the Shadowsocks-2022 inbound "+tag+" taking users; made by W-UI, holds no one", gid); err != nil {
		return err
	}
	var core struct {
		Config map[string]any `json:"config"`
	}
	if err := p.do(ctx, http.MethodGet, fmt.Sprintf("/api/core/%d", coreID), nil, &core); err != nil {
		return err
	}
	if err := p.do(ctx, http.MethodPut, fmt.Sprintf("/api/core/%d?restart_nodes=true", coreID), map[string]any{
		"name": coreName, "config": core.Config, "exclude_inbound_tags": []string{}, "fallbacks_inbound_tags": []string{},
	}, nil); err != nil {
		return err
	}
	return p.nodesRunAfter(ctx)
}

