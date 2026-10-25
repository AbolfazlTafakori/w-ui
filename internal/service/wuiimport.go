package service

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/handover"
)

// Bringing in the customers a W-UI node had of its own before it was one
// (internal/handover).
//
// Unlike a 3x-ui user, which stays where it is and is switched on and off
// over that panel's API, a W-UI node's own tunnels and customers are made
// here as they are -- the same keys, ports, addresses and passwords -- and
// the node then hands its rows over to this panel's sync. Nothing a customer
// holds changes; their subscription link is this panel's from then on.
//
// A tunnel comes with every customer on it, and a customer with every tunnel
// they are on: whatever was asked for is brought in with all it is tied to.

// handoverGroup is the tunnels and customers that have to move together.
type handoverGroup struct {
	tunnels []handover.Tunnel
	clients []handover.Client
	reason  string
}

// wuiKey names a W-UI node's customer among its users: their id there.
func wuiKey(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// wuiCandidates is a W-UI node's own customers, as candidates.
func (s *Clients) wuiCandidates(ctx context.Context, node model.Node) ([]ImportCandidate, error) {
	h, err := s.remote.Handover(ctx, node)
	if err != nil {
		return nil, err
	}
	groups, err := s.handoverGroups(ctx, node, h)
	if err != nil {
		return nil, err
	}
	out := []ImportCandidate{}
	for _, g := range groups {
		names := map[uint]string{}
		for _, t := range g.tunnels {
			names[t.LocalID] = t.Name
		}
		for _, c := range g.clients {
			cand := ImportCandidate{
				Key: wuiKey(c.LocalID), Name: c.Name, Note: c.Note,
				Enabled:    c.Status == model.StatusActive,
				QuotaBytes: c.QuotaBytes, UsedBytes: c.UsedBytes, ExpiresAt: c.ExpiresAt,
				DeviceLimit: c.DeviceLimit, Reason: g.reason, Inbounds: []string{},
			}
			if c.StartOnFirstUse {
				cand.DurationDays = c.DurationDays
			}
			seen := map[uint]bool{}
			for _, a := range c.Accounts {
				if !seen[a.TunnelLocalID] {
					seen[a.TunnelLocalID] = true
					cand.Inbounds = append(cand.Inbounds, names[a.TunnelLocalID])
				}
			}
			out = append(out, cand)
		}
	}
	return out, nil
}

// handoverGroups splits what a node hands over into what moves together,
// each with why it cannot be brought in, if it cannot.
func (s *Clients) handoverGroups(ctx context.Context, node model.Node, h handover.Handover) ([]handoverGroup, error) {
	// Tunnels and customers are joined by the files between them; a group
	// is everything reachable from one tunnel.
	parent := map[uint]uint{}
	var find func(uint) uint
	find = func(x uint) uint {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, t := range h.Tunnels {
		parent[t.LocalID] = t.LocalID
	}
	for _, c := range h.Clients {
		var first uint
		for _, a := range c.Accounts {
			if _, ok := parent[a.TunnelLocalID]; !ok {
				return nil, fmt.Errorf("%w: the node sent a file on a tunnel it did not send", ErrInvalid)
			}
			if first == 0 {
				first = a.TunnelLocalID
				continue
			}
			parent[find(a.TunnelLocalID)] = find(first)
		}
	}
	byRoot := map[uint]*handoverGroup{}
	var order []uint
	for _, t := range h.Tunnels {
		r := find(t.LocalID)
		if byRoot[r] == nil {
			byRoot[r] = &handoverGroup{}
			order = append(order, r)
		}
		byRoot[r].tunnels = append(byRoot[r].tunnels, t)
	}
	for _, c := range h.Clients {
		if len(c.Accounts) == 0 {
			continue
		}
		g := byRoot[find(c.Accounts[0].TunnelLocalID)]
		g.clients = append(g.clients, c)
	}

	var here []model.Interface
	if err := s.db.WithContext(ctx).Where("node_id = ?", node.ID).Find(&here).Error; err != nil {
		return nil, fmt.Errorf("service: read the node's tunnels: %w", err)
	}
	out := make([]handoverGroup, 0, len(order))
	for _, r := range order {
		g := byRoot[r]
		for _, t := range g.tunnels {
			if reason := handoverClash(t, here); reason != "" {
				g.reason = reason
				break
			}
		}
		for _, c := range g.clients {
			if g.reason == "" && c.Elsewhere {
				g.reason = fmt.Sprintf("%s also has a file on an Xray inbound or another panel's tunnel there, which cannot be handed over", c.Name)
			}
		}
		out = append(out, *g)
	}
	return out, nil
}

// handoverClash says why a node's tunnel cannot be made here beside the
// tunnels this panel already has on that node, or "" when it can. The node
// refused the same on its side, so a clash means two panels each put
// something there.
func handoverClash(t handover.Tunnel, here []model.Interface) string {
	want, err := netip.ParsePrefix(t.Subnet)
	if err != nil {
		return fmt.Sprintf("tunnel %s has a subnet that cannot be read (%s)", t.Name, t.Subnet)
	}
	mine := model.Interface{Protocol: t.Protocol}
	if t.OpenVPN != nil {
		mine.OpenVPN = model.JSON(*t.OpenVPN)
	}
	if t.OpenConnect != nil {
		mine.OpenConnect = model.JSON(*t.OpenConnect)
	}
	for _, o := range here {
		if o.Name == t.Name {
			return fmt.Sprintf("this panel already has a tunnel named %s on that server", t.Name)
		}
		if one := singleTunnel(t.Protocol); one && o.Protocol == t.Protocol {
			return fmt.Sprintf("this panel already has an %s tunnel (%s) on that server, and a server carries one",
				strings.ToUpper(string(t.Protocol)), o.Name)
		}
		if o.ListenPort == t.ListenPort {
			for _, a := range ifaceTransports(o) {
				for _, b := range ifaceTransports(mine) {
					if a == b {
						return fmt.Sprintf("%s %d of tunnel %s is the port of %s, made here on that server", b, t.ListenPort, t.Name, o.Name)
					}
				}
			}
		}
		if have, err := netip.ParsePrefix(o.Subnet); err == nil && have.Overlaps(want) {
			return fmt.Sprintf("the subnet of tunnel %s overlaps %s's, made here on that server", t.Name, o.Name)
		}
	}
	return ""
}

// singleTunnel is a protocol a server carries one tunnel of.
func singleTunnel(p model.Protocol) bool {
	switch p {
	case model.ProtocolL2TP, model.ProtocolPPTP, model.ProtocolIKEv2, model.ProtocolOpenConnect:
		return true
	}
	return false
}

// importWUI brings in the customers asked for by key from a W-UI node, with
// everything they are tied to; none named brings in all the node has,
// tunnels without customers included.
func (s *Clients) importWUI(ctx context.Context, node model.Node, keys []string) ([]ImportResult, error) {
	h, err := s.remote.Handover(ctx, node)
	if err != nil {
		return nil, err
	}
	groups, err := s.handoverGroups(ctx, node, h)
	if err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, k := range keys {
		asked[k] = true
	}

	var out []ImportResult
	var take []handoverGroup
	found := map[string]bool{}
	for _, g := range groups {
		wanted := len(keys) == 0
		for _, c := range g.clients {
			if asked[wuiKey(c.LocalID)] {
				wanted = true
				found[wuiKey(c.LocalID)] = true
			}
		}
		if !wanted {
			continue
		}
		if g.reason != "" {
			for _, c := range g.clients {
				out = append(out, ImportResult{Key: wuiKey(c.LocalID), Name: c.Name, Error: g.reason})
			}
			continue
		}
		take = append(take, g)
	}
	for _, k := range keys {
		if !found[k] {
			// Claimed already, or never the node's own.
			out = append(out, ImportResult{Key: k, Error: "not one of that server's own customers; it may have been brought in already"})
		}
	}
	if len(take) == 0 {
		return out, nil
	}

	made, claim, err := s.makeHandedOver(ctx, node, take)
	if err != nil {
		return nil, err
	}
	if err := s.remote.ClaimHandover(ctx, node, claim); err != nil {
		// The node keeps them: what was made here would otherwise be a
		// second copy of tunnels the node still runs as its own.
		if uerr := s.unmakeHandedOver(ctx, made); uerr != nil {
			s.log.Error("could not undo a handover the node refused", "node", node.Name, "error", uerr)
		}
		return nil, fmt.Errorf("%w: %s did not hand its customers over: %v", ErrInvalid, node.Name, err)
	}
	for _, g := range take {
		for _, c := range g.clients {
			out = append(out, ImportResult{Key: wuiKey(c.LocalID), Name: c.Name, ClientID: made.clients[c.LocalID]})
		}
	}
	s.log.Info("customers brought in from a W-UI node",
		"node", node.Name, "tunnels", len(made.tunnels), "customers", len(made.clients))
	sort.SliceStable(out, func(i, j int) bool { return out[i].Error == "" && out[j].Error != "" })
	return out, nil
}

// handedOver is what was made here for a handover, by the node's ids.
type handedOver struct {
	tunnels  map[uint]uint
	clients  map[uint]uint
	accounts map[uint]uint
	subnets  map[uint]string
	ips      map[uint][]string
}

// makeHandedOver makes the groups' tunnels, customers and files here, as the
// node has them, and says what the node is to claim.
func (s *Clients) makeHandedOver(ctx context.Context, node model.Node, take []handoverGroup) (*handedOver, handover.Claim, error) {
	made := &handedOver{tunnels: map[uint]uint{}, clients: map[uint]uint{}, accounts: map[uint]uint{},
		subnets: map[uint]string{}, ips: map[uint][]string{}}
	var claim handover.Claim
	owner := OwnerOf(ctx)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		protocols := map[uint]model.Protocol{}
		for _, g := range take {
			for _, t := range g.tunnels {
				iface := handedTunnel(node.ID, t)
				if err := database.CreateKeepingSwitch(tx, &iface, t.Enabled); err != nil {
					return fmt.Errorf("make tunnel %s: %w", t.Name, err)
				}
				made.tunnels[t.LocalID] = iface.ID
				made.subnets[iface.ID] = iface.Subnet
				protocols[t.LocalID] = t.Protocol
				claim.Tunnels = append(claim.Tunnels, handover.IDPair{LocalID: t.LocalID, OriginID: iface.ID})
			}
		}
		for _, g := range take {
			for _, c := range g.clients {
				client, err := s.handedClient(tx, owner, c, protocols)
				if err != nil {
					return err
				}
				if err := tx.Create(&client).Error; err != nil {
					return fmt.Errorf("make customer %s: %w", c.Name, err)
				}
				if err := setGroups(ctx, tx, client.ID, c.Groups); err != nil {
					return err
				}
				made.clients[c.LocalID] = client.ID
				claim.Clients = append(claim.Clients, handover.IDPair{LocalID: c.LocalID, OriginID: client.ID,
					Used: c.UsedBytes, Up: c.UpBytes, Down: c.DownBytes})
				for _, a := range c.Accounts {
					acc := model.Account{
						ClientID: client.ID, InterfaceID: made.tunnels[a.TunnelLocalID], NodeID: node.ID,
						DeviceName: a.DeviceName, IP: a.IP, Enabled: a.Enabled,
						PrivateKey: a.PrivateKey, PublicKey: a.PublicKey, PresharedKey: a.PresharedKey,
						Username: a.Username, Secret: a.Secret, CertPEM: a.CertPEM, CertKeyPEM: a.CertKeyPEM,
						UpBytes: a.UpBytes, DownBytes: a.DownBytes,
					}
					if err := database.CreateKeepingSwitch(tx, &acc, a.Enabled); err != nil {
						return fmt.Errorf("make %s's file %s: %w", c.Name, a.DeviceName, err)
					}
					if err := tx.Create(&model.IPLease{AccountID: acc.ID, ClientID: client.ID, IP: acc.IP,
						FromTS: time.Now().UTC()}).Error; err != nil {
						return fmt.Errorf("record address lease: %w", err)
					}
					made.accounts[a.LocalID] = acc.ID
					made.ips[acc.InterfaceID] = append(made.ips[acc.InterfaceID], acc.IP)
					claim.Accounts = append(claim.Accounts, handover.IDPair{LocalID: a.LocalID, OriginID: acc.ID,
						Up: a.UpBytes, Down: a.DownBytes})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, claim, fmt.Errorf("service: %w", err)
	}
	for id, subnet := range made.subnets {
		if _, err := s.pools.Add(id, subnet); err != nil {
			_ = s.unmakeHandedOver(ctx, made) // the error returned is the one that matters
			return nil, claim, err
		}
		for _, ip := range made.ips[id] {
			if err := s.pools.Replay(id, ip); err != nil {
				_ = s.unmakeHandedOver(ctx, made)
				return nil, claim, err
			}
		}
	}
	return made, claim, nil
}

// handedTunnel is a node's tunnel as a row here.
func handedTunnel(nodeID uint, t handover.Tunnel) model.Interface {
	iface := model.Interface{
		NodeID: nodeID, Name: t.Name, Protocol: t.Protocol, Enabled: t.Enabled,
		ListenPort: t.ListenPort, Subnet: t.Subnet, EndpointHost: t.EndpointHost,
		MTU: t.MTU, DNS: t.DNS, NATInterface: t.NATInterface, Mode: t.Mode,
		PrivateKey: t.PrivateKey, PublicKey: t.PublicKey,
	}
	if iface.Mode == "" {
		iface.Mode = model.ModeStandard
	}
	if t.AWG != nil {
		iface.AWG = model.JSON(*t.AWG)
	}
	if t.OpenVPN != nil {
		iface.OpenVPN = model.JSON(*t.OpenVPN)
	}
	if t.L2TP != nil {
		iface.L2TP = model.JSON(*t.L2TP)
	}
	if t.IKEv2 != nil {
		iface.IKEv2 = model.JSON(*t.IKEv2)
	}
	if t.OpenConnect != nil {
		iface.OpenConnect = model.JSON(*t.OpenConnect)
	}
	return iface
}

// handedClient is a node's customer as a row here, with their plan as it
// stood there. Their subscription id comes too, unless a customer here
// already has it.
func (s *Clients) handedClient(tx *gorm.DB, owner uint, c handover.Client, protocols map[uint]model.Protocol) (model.Client, error) {
	client := model.Client{
		OwnerID: owner, Name: importName(c.Name, wuiKey(c.LocalID)), Note: c.Note,
		Group: firstGroup(c.Groups), TelegramID: c.TelegramID, BaleID: c.BaleID,
		Protocol:   protocols[c.Accounts[0].TunnelLocalID],
		QuotaBytes: c.QuotaBytes, UsedBytes: c.UsedBytes, UpBytes: c.UpBytes, DownBytes: c.DownBytes,
		ExpiresAt: c.ExpiresAt, StartOnFirstUse: c.StartOnFirstUse,
		DurationDays: c.DurationDays, DurationHours: c.DurationHours, ActivatedAt: c.ActivatedAt,
		DeviceLimit: c.DeviceLimit, RateBitsPerSec: c.RateBitsPerSec,
		ResetCycle: c.ResetCycle, LastResetAt: c.LastResetAt,
		Status: c.Status, SubToken: c.SubToken,
	}
	if client.Status == "" {
		client.Status = model.StatusActive
	}
	taken := client.SubToken == ""
	if !taken {
		var n int64
		if err := tx.Model(&model.Client{}).Where("sub_token = ?", client.SubToken).Count(&n).Error; err != nil {
			return client, err
		}
		taken = n > 0
	}
	if taken {
		tok, err := newSubToken()
		if err != nil {
			return client, err
		}
		client.SubToken = tok
	}
	return client, nil
}

// unmakeHandedOver removes what makeHandedOver made, for a handover the node
// did not take.
func (s *Clients) unmakeHandedOver(ctx context.Context, made *handedOver) error {
	clients := make([]uint, 0, len(made.clients))
	for _, id := range made.clients {
		clients = append(clients, id)
	}
	tunnels := make([]uint, 0, len(made.tunnels))
	for _, id := range made.tunnels {
		tunnels = append(tunnels, id)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(clients) > 0 {
			for _, q := range []struct {
				row   any
				where string
			}{
				{&model.IPLease{}, "client_id IN ?"},
				{&model.Account{}, "client_id IN ?"},
				{&model.ClientGroup{}, "client_id IN ?"},
				{&model.Client{}, "id IN ?"},
			} {
				if err := tx.Where(q.where, clients).Delete(q.row).Error; err != nil {
					return err
				}
			}
		}
		if len(tunnels) > 0 {
			if err := tx.Where("id IN ?", tunnels).Delete(&model.Interface{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	for _, id := range tunnels {
		s.pools.Remove(id)
	}
	return err
}
