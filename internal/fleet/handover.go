package fleet

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
	"github.com/abolfazl/w-ui/internal/handover"
)

// The node's half of a handover (internal/handover): it reads what it has of
// its own, and marks what the managing panel took over as managed.

// less is a counter with what was handed over taken off it, never below zero.
func less(column string, by uint64) any {
	return gorm.Expr("CASE WHEN "+column+" > ? THEN "+column+" - ? ELSE 0 END", by, by)
}

// ownTunnels are the tunnels this panel made itself on its own server: not
// handed to it by a managing panel, and not a proxy inbound.
func ownTunnels(tx *gorm.DB, nodeID uint) *gorm.DB {
	return tx.Where("node_id = ? AND managed = ? AND protocol <> ?", nodeID, false, model.ProtocolXray)
}

// Handover reads this panel's own tunnels and the customers on them.
func (s *NodeSync) Handover(ctx context.Context, nodeID uint) (handover.Handover, error) {
	db := s.db.WithContext(ctx)
	var tunnels []model.Interface
	if err := ownTunnels(db, nodeID).Order("id").Find(&tunnels).Error; err != nil {
		return handover.Handover{}, fmt.Errorf("read own tunnels: %w", err)
	}
	out := handover.Handover{Tunnels: []handover.Tunnel{}, Clients: []handover.Client{}}
	if len(tunnels) == 0 {
		return out, nil
	}
	ids := make([]uint, 0, len(tunnels))
	for _, t := range tunnels {
		ids = append(ids, t.ID)
		out.Tunnels = append(out.Tunnels, handoverTunnel(t))
	}

	var accounts []model.Account
	if err := db.Where("interface_id IN ?", ids).Order("id").Find(&accounts).Error; err != nil {
		return handover.Handover{}, fmt.Errorf("read own accounts: %w", err)
	}
	byClient := map[uint][]model.Account{}
	var clientIDs []uint
	for _, a := range accounts {
		if _, ok := byClient[a.ClientID]; !ok {
			clientIDs = append(clientIDs, a.ClientID)
		}
		byClient[a.ClientID] = append(byClient[a.ClientID], a)
	}
	if len(clientIDs) == 0 {
		return out, nil
	}
	var clients []model.Client
	if err := db.Where("id IN ? AND origin_id = 0", clientIDs).Order("id").Find(&clients).Error; err != nil {
		return handover.Handover{}, fmt.Errorf("read own customers: %w", err)
	}
	var groups []model.ClientGroup
	if err := db.Where("client_id IN ?", clientIDs).Order("name").Find(&groups).Error; err != nil {
		return handover.Handover{}, fmt.Errorf("read groups: %w", err)
	}
	// A customer with a file on a tunnel that is not handed over.
	var elsewhere []uint
	if err := db.Model(&model.Account{}).Where("client_id IN ? AND interface_id NOT IN ?", clientIDs, ids).
		Distinct().Pluck("client_id", &elsewhere).Error; err != nil {
		return handover.Handover{}, fmt.Errorf("read files elsewhere: %w", err)
	}
	away := map[uint]bool{}
	for _, id := range elsewhere {
		away[id] = true
	}
	groupsOf := map[uint][]string{}
	for _, g := range groups {
		groupsOf[g.ClientID] = append(groupsOf[g.ClientID], g.Name)
	}
	for _, c := range clients {
		hc := handover.Client{
			LocalID: c.ID, Name: c.Name, Note: c.Note, Groups: groupsOf[c.ID],
			TelegramID: c.TelegramID, BaleID: c.BaleID,
			QuotaBytes: c.QuotaBytes, UsedBytes: c.UsedBytes, UpBytes: c.UpBytes, DownBytes: c.DownBytes,
			ExpiresAt: c.ExpiresAt, StartOnFirstUse: c.StartOnFirstUse, DurationDays: c.DurationDays,
			DurationHours: c.DurationHours, ActivatedAt: c.ActivatedAt, DeviceLimit: c.DeviceLimit,
			RateBitsPerSec: c.RateBitsPerSec, ResetCycle: c.ResetCycle, LastResetAt: c.LastResetAt,
			Status: c.Status, SubToken: c.SubToken, Elsewhere: away[c.ID],
		}
		for _, a := range byClient[c.ID] {
			hc.Accounts = append(hc.Accounts, handover.Account{
				LocalID: a.ID, TunnelLocalID: a.InterfaceID, DeviceName: a.DeviceName, IP: a.IP,
				Enabled: a.Enabled, PrivateKey: a.PrivateKey, PublicKey: a.PublicKey,
				PresharedKey: a.PresharedKey, Username: a.Username, Secret: a.Secret,
				CertPEM: a.CertPEM, CertKeyPEM: a.CertKeyPEM, UpBytes: a.UpBytes, DownBytes: a.DownBytes,
			})
		}
		out.Clients = append(out.Clients, hc)
	}
	return out, nil
}

// handoverTunnel is a tunnel as the managing panel takes it: everything a
// sync carries, and the IKEv2 authority's key, which a sync never does.
func handoverTunnel(t model.Interface) handover.Tunnel {
	out := handover.Tunnel{
		LocalID: t.ID, Name: t.Name, Protocol: t.Protocol, Enabled: t.Enabled,
		ListenPort: t.ListenPort, Subnet: t.Subnet, EndpointHost: t.EndpointHost,
		MTU: t.MTU, DNS: t.DNS, NATInterface: t.NATInterface, Mode: t.Mode,
		PrivateKey: t.PrivateKey, PublicKey: t.PublicKey,
	}
	if v := t.AWG.V; v != (model.AWGParams{}) {
		out.AWG = &v
	}
	if v := t.OpenVPN.V; v.CACert != "" {
		out.OpenVPN = &v
	}
	switch t.Protocol {
	case model.ProtocolL2TP:
		v := t.L2TP.V
		out.L2TP = &v
	case model.ProtocolIKEv2:
		v := t.IKEv2.V
		out.IKEv2 = &v
	case model.ProtocolOpenConnect:
		v := t.OpenConnect.V
		out.OpenConnect = &v
	}
	return out
}

// Claim marks the rows the managing panel took over as managed, under its
// ids. All of it or none: a claim that names a tunnel and leaves out one of
// its customers, or a customer and leaves out one of their files, is
// refused, because the next sync would cut off whatever was left out.
func (s *NodeSync) Claim(ctx context.Context, nodeID uint, c handover.Claim) error {
	if len(c.Tunnels) == 0 {
		return fault.Field("tunnels", "nothing to hand over")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tunnelIDs := make([]uint, 0, len(c.Tunnels))
		for _, p := range c.Tunnels {
			if p.LocalID == 0 || p.OriginID == 0 {
				return fault.Field("tunnels", "a tunnel without both ids")
			}
			tunnelIDs = append(tunnelIDs, p.LocalID)
		}
		var own []model.Interface
		if err := ownTunnels(tx, nodeID).Where("id IN ?", tunnelIDs).Find(&own).Error; err != nil {
			return err
		}
		if len(own) != len(tunnelIDs) {
			return fault.Field("tunnels", "a tunnel named is not one of this panel's own; it may have been handed over already")
		}

		// Every file on those tunnels, and every customer they belong to, must
		// be named.
		var accounts []model.Account
		if err := tx.Where("interface_id IN ?", tunnelIDs).Find(&accounts).Error; err != nil {
			return err
		}
		accOrigin := map[uint]uint{}
		for _, p := range c.Accounts {
			accOrigin[p.LocalID] = p.OriginID
		}
		cliOrigin := map[uint]uint{}
		for _, p := range c.Clients {
			cliOrigin[p.LocalID] = p.OriginID
		}
		var missing []string
		for _, a := range accounts {
			if accOrigin[a.ID] == 0 {
				missing = append(missing, fmt.Sprintf("file %d", a.ID))
			}
			if cliOrigin[a.ClientID] == 0 {
				missing = append(missing, fmt.Sprintf("customer %d", a.ClientID))
			}
		}
		// And a customer handed over has no file left on a tunnel that is not.
		if len(c.Clients) > 0 {
			ids := make([]uint, 0, len(c.Clients))
			for _, p := range c.Clients {
				ids = append(ids, p.LocalID)
			}
			var stray int64
			if err := tx.Model(&model.Account{}).Where("client_id IN ? AND interface_id NOT IN ?", ids, tunnelIDs).
				Count(&stray).Error; err != nil {
				return err
			}
			if stray > 0 {
				missing = append(missing, "a tunnel those customers are also on")
			}
		}
		if len(missing) > 0 {
			return fault.Field("tunnels", "the handover leaves out %s; a tunnel goes with every customer on it", strings.Join(dedupe(missing), ", "))
		}

		for _, p := range c.Tunnels {
			if err := tx.Model(&model.Interface{}).Where("id = ?", p.LocalID).
				Updates(map[string]any{"managed": true, "origin_id": p.OriginID}).Error; err != nil {
				return fmt.Errorf("hand over tunnel %d: %w", p.LocalID, err)
			}
		}
		for _, p := range c.Clients {
			// What the managing panel keeps from now on, it keeps: the plan is
			// cleared here, and the name is the id, as a sync would make it.
			if err := tx.Model(&model.Client{}).Where("id = ?", p.LocalID).Updates(map[string]any{
				"origin_id": p.OriginID, "name": fmt.Sprintf("origin-%d", p.OriginID),
				"quota_bytes": 0, "expires_at": nil, "start_on_first_use": false,
				"duration_days": 0, "duration_hours": 0, "owner_id": 0,
				"used_bytes": less("used_bytes", p.Used), "up_bytes": less("up_bytes", p.Up),
				"down_bytes": less("down_bytes", p.Down),
			}).Error; err != nil {
				return fmt.Errorf("hand over customer %d: %w", p.LocalID, err)
			}
			if err := tx.Where("client_id = ?", p.LocalID).Delete(&model.ClientGroup{}).Error; err != nil {
				return err
			}
		}
		for _, p := range c.Accounts {
			if err := tx.Model(&model.Account{}).Where("id = ? AND interface_id IN ?", p.LocalID, tunnelIDs).
				Updates(map[string]any{"origin_id": p.OriginID,
					"up_bytes": less("up_bytes", p.Up), "down_bytes": less("down_bytes", p.Down)}).Error; err != nil {
				return fmt.Errorf("hand over file %d: %w", p.LocalID, err)
			}
		}
		if s.log != nil {
			s.log.Info("tunnels handed over to the managing panel",
				"tunnels", len(c.Tunnels), "customers", len(c.Clients), "files", len(c.Accounts))
		}
		return nil
	})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
