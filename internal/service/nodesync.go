package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The state one panel hands another.
//
// A node is another W-UI panel, so the thing being spoken is this panel's own
// API rather than a second protocol nobody exercises. What crosses the wire is
// the desired state in full — the tunnel and every peer that should be on it —
// and never a command. The node makes itself match, which means a sync that
// runs twice is harmless, a node that was offline catches up on its own, and
// nothing has to be replayed after a restart.
//
// Deliberately absent from the payload: the allowance, the expiry, the reset
// cycle, the customer's name. A node serves peers. The plan behind them is one
// number on the panel that sold it, spent across every node the customer
// reaches, and a node that held its own copy would enforce its own share of it.

// NodeState is everything one tunnel on a node should be running.
type NodeState struct {
	Interface NodeInterface `json:"interface"`
	Clients   []NodeClient  `json:"clients"`
}

// NodeInterface is the tunnel itself.
type NodeInterface struct {
	OriginID     uint                 `json:"originId"`
	Name         string               `json:"name"`
	Protocol     model.Protocol       `json:"protocol"`
	Enabled      bool                 `json:"enabled"`
	ListenPort   int                  `json:"listenPort"`
	Subnet       string               `json:"subnet"`
	EndpointHost string               `json:"endpointHost"`
	MTU          int                  `json:"mtu"`
	DNS          string               `json:"dns"`
	NATInterface string               `json:"natInterface"`
	Mode         model.InterfaceMode  `json:"mode"`
	PrivateKey   string               `json:"privateKey,omitempty"`
	PublicKey    string               `json:"publicKey,omitempty"`
	AWG          *model.AWGParams     `json:"awg,omitempty"`
	OpenVPN      *model.OpenVPNParams `json:"openvpn,omitempty"`
}

// NodeClient is one customer's presence on this node.
//
// Enabled is the whole of the enforcement that reaches a node. The panel that
// holds the records decides — out of allowance, past their date, switched off
// by an operator — and the answer arrives here as a boolean. That is what makes
// a customer who has spent their allowance stop working on every node at once
// rather than only on the one that happened to count the last byte.
type NodeClient struct {
	OriginID       uint   `json:"originId"`
	Enabled        bool   `json:"enabled"`
	RateBitsPerSec uint64 `json:"rateBitsPerSec"`
	// DeviceLimit is how many connections the plan allows at once. The panel
	// holds the customer to it across every server; the node keeps it only
	// to fall back on its own view while the panel is out of touch.
	DeviceLimit int           `json:"deviceLimit"`
	Accounts    []NodeAccount `json:"accounts"`
}

// NodeAccount is one device's credentials on this tunnel.
type NodeAccount struct {
	OriginID     uint   `json:"originId"`
	DeviceName   string `json:"deviceName"`
	IP           string `json:"ip"`
	Enabled      bool   `json:"enabled"`
	PrivateKey   string `json:"privateKey,omitempty"`
	PublicKey    string `json:"publicKey,omitempty"`
	PresharedKey string `json:"presharedKey,omitempty"`
	Username     string `json:"username,omitempty"`
	Secret       string `json:"secret,omitempty"`
	// HeldUntil, when set, is a device the panel has decided is over its
	// plan's connections at once: the node keeps it off until then. Carried
	// in every push as well as by the immediate call, so a node that missed
	// the call still learns of it.
	HeldUntil *time.Time `json:"heldUntil,omitempty"`
}

// NodeSession is one credential live on a node, as the node reports it to
// the panel that owns the customer -- a few bytes every few seconds, which
// is what lets the panel count a customer's connections across servers.
type NodeSession struct {
	OriginID    uint `json:"originId"`
	Connections int  `json:"connections"`
	// AgeSeconds is how long the session has been live, as an age rather
	// than a time so the two machines' clocks need not agree.
	AgeSeconds int      `json:"ageSeconds"`
	Addrs      []string `json:"addrs,omitempty"`
}

// NodeHold is one device the panel wants held off on a node.
type NodeHold struct {
	OriginID uint      `json:"originId"`
	Until    time.Time `json:"until"`
}

// NodeUsage is what one customer spent on this node since it was last asked.
type NodeUsage struct {
	OriginID uint   `json:"originId"`
	Bytes    uint64 `json:"bytes"`
	Up       uint64 `json:"up"`
	Down     uint64 `json:"down"`
}

// NodeSync applies and reports state on the panel acting as a node.
//
// It does not touch the address allocator: a node is told which addresses to
// use rather than choosing any, and every account on a managed tunnel came from
// the panel that owns it. The allocator is rebuilt from the stored rows at boot
// like any other interface's.
type NodeSync struct {
	db  *gorm.DB
	log *slog.Logger

	// OnHold is how a hold the panel decided reaches this server's data
	// plane: the account's local id and until when. Set by the panel; nil
	// on a CLI.
	OnHold func(ctx context.Context, accountID uint, until time.Time)

	// OnRemoveInterface takes a withdrawn tunnel's device down. Set by the
	// panel to the same teardown its own deletions use; nil on a CLI.
	OnRemoveInterface func(ctx context.Context, iface *model.Interface)
}

func NewNodeSync(db *gorm.DB, log *slog.Logger) *NodeSync {
	return &NodeSync{db: db, log: log}
}

// Apply makes this panel's records match what it was handed.
//
// Everything is keyed by the origin id rather than by name or address, because
// those are the things an operator changes. A tunnel renamed centrally must
// move this node's existing interface rather than leaving the old one behind
// and building a second.
func (s *NodeSync) Apply(ctx context.Context, nodeID uint, state NodeState) error {
	if state.Interface.OriginID == 0 {
		return invalidField("interface.originId", "the tunnel has no id on the panel that sent it")
	}
	if state.Interface.Name = strings.TrimSpace(state.Interface.Name); state.Interface.Name == "" {
		return invalidField("interface.name", "a tunnel needs a name")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		iface, err := s.upsertInterface(tx, nodeID, state.Interface)
		if err != nil {
			return err
		}
		return s.upsertClients(tx, iface, state.Clients)
	})
}

func (s *NodeSync) upsertInterface(tx *gorm.DB, nodeID uint, in NodeInterface) (*model.Interface, error) {
	var iface model.Interface
	err := tx.Where("node_id = ? AND managed = ? AND origin_id = ?", nodeID, true, in.OriginID).
		Limit(1).Find(&iface).Error
	if err != nil {
		return nil, fmt.Errorf("read managed interface: %w", err)
	}

	iface.NodeID = nodeID
	iface.Managed = true
	iface.OriginID = in.OriginID
	iface.Name = in.Name
	iface.Protocol = in.Protocol
	iface.Enabled = in.Enabled
	iface.ListenPort = in.ListenPort
	iface.Subnet = in.Subnet
	iface.EndpointHost = in.EndpointHost
	iface.MTU = in.MTU
	iface.DNS = in.DNS
	iface.NATInterface = in.NATInterface
	iface.Mode = in.Mode
	iface.PrivateKey = in.PrivateKey
	iface.PublicKey = in.PublicKey
	if in.AWG != nil {
		iface.AWG = model.JSON(*in.AWG)
	}
	if in.OpenVPN != nil {
		iface.OpenVPN = model.JSON(*in.OpenVPN)
	}

	if err := tx.Save(&iface).Error; err != nil {
		return nil, fmt.Errorf("store managed interface: %w", err)
	}
	return &iface, nil
}

func (s *NodeSync) upsertClients(tx *gorm.DB, iface *model.Interface, want []NodeClient) error {
	var existing []model.Client
	if err := tx.Preload("Accounts").
		Where("origin_id > 0").Find(&existing).Error; err != nil {
		return fmt.Errorf("read managed clients: %w", err)
	}
	byOrigin := make(map[uint]*model.Client, len(existing))
	for i := range existing {
		byOrigin[existing[i].OriginID] = &existing[i]
	}

	seen := map[uint]bool{}
	for _, wc := range want {
		if wc.OriginID == 0 {
			continue
		}
		seen[wc.OriginID] = true

		c := byOrigin[wc.OriginID]
		if c == nil {
			c = &model.Client{OriginID: wc.OriginID}
		}
		// Named after the id, not the customer. A node may be rented from
		// somebody else and has no business learning who is on it.
		c.Name = fmt.Sprintf("origin-%d", wc.OriginID)
		c.Protocol = iface.Protocol
		c.RateBitsPerSec = wc.RateBitsPerSec
		// No allowance and no expiry: this node counts, the panel that sold the
		// plan decides. Enabled is the decision arriving.
		c.QuotaBytes = 0
		c.ExpiresAt = nil
		// 0 is unlimited -- also what a panel older than this field sends,
		// and such a panel never held anybody, so the fallback holds nobody.
		c.DeviceLimit = wc.DeviceLimit
		if c.DeviceLimit < 0 {
			c.DeviceLimit = 0
		}
		if wc.Enabled {
			c.Status = model.StatusActive
		} else {
			c.Status = model.StatusDisabled
		}
		if err := tx.Save(c).Error; err != nil {
			return fmt.Errorf("store managed client %d: %w", wc.OriginID, err)
		}
		if err := s.upsertAccounts(tx, iface, c, wc.Accounts); err != nil {
			return err
		}
	}

	// Anything the panel stopped sending is gone: a customer deleted centrally
	// must stop working here, and leaving the peer behind would be exactly the
	// free service this is meant to prevent.
	//
	// Only from this tunnel: the push is per tunnel, and a customer on the
	// node's other tunnel is simply not in this one. Their row goes when
	// nothing on any tunnel here is left for it.
	for _, c := range existing {
		if seen[c.OriginID] {
			continue
		}
		if err := tx.Where("client_id = ? AND interface_id = ?", c.ID, iface.ID).
			Delete(&model.Account{}).Error; err != nil {
			return fmt.Errorf("remove withdrawn accounts: %w", err)
		}
		var left int64
		if err := tx.Model(&model.Account{}).Where("client_id = ?", c.ID).Count(&left).Error; err != nil {
			return fmt.Errorf("count remaining accounts: %w", err)
		}
		if left == 0 {
			if err := tx.Delete(&model.Client{}, c.ID).Error; err != nil {
				return fmt.Errorf("remove withdrawn client: %w", err)
			}
		}
	}
	return nil
}

func (s *NodeSync) upsertAccounts(
	tx *gorm.DB, iface *model.Interface, c *model.Client, want []NodeAccount,
) error {
	var existing []model.Account
	if err := tx.Where("client_id = ? AND interface_id = ?", c.ID, iface.ID).
		Find(&existing).Error; err != nil {
		return fmt.Errorf("read managed accounts: %w", err)
	}
	byOrigin := make(map[uint]*model.Account, len(existing))
	for i := range existing {
		byOrigin[existing[i].OriginID] = &existing[i]
	}

	seen := map[uint]bool{}
	for _, wa := range want {
		if wa.OriginID == 0 {
			continue
		}
		seen[wa.OriginID] = true

		a := byOrigin[wa.OriginID]
		if a == nil {
			a = &model.Account{OriginID: wa.OriginID}
		}
		a.ClientID = c.ID
		a.InterfaceID = iface.ID
		a.NodeID = iface.NodeID
		a.DeviceName = wa.DeviceName
		a.IP = wa.IP
		a.Enabled = wa.Enabled
		a.PrivateKey = wa.PrivateKey
		a.PublicKey = wa.PublicKey
		a.PresharedKey = wa.PresharedKey
		a.Username = wa.Username
		a.Secret = wa.Secret
		if err := tx.Save(a).Error; err != nil {
			return fmt.Errorf("store managed account %d: %w", wa.OriginID, err)
		}
		if wa.HeldUntil != nil && s.OnHold != nil && time.Now().Before(*wa.HeldUntil) {
			s.OnHold(context.Background(), a.ID, *wa.HeldUntil)
		}
	}

	for _, a := range existing {
		if seen[a.OriginID] {
			continue
		}
		if err := tx.Delete(&model.Account{}, a.ID).Error; err != nil {
			return fmt.Errorf("remove withdrawn account: %w", err)
		}
	}
	return nil
}

// Drain reports what each customer spent here and resets the counters.
//
// Read and zero in one transaction, the same shape the kernel counters use, so
// bytes that arrive between the two cannot be counted twice or lost. The panel
// asking is about to add these to one total that spans every node, and a number
// returned twice would bill a customer for traffic they never sent.
func (s *NodeSync) Drain(ctx context.Context) ([]NodeUsage, error) {
	var out []NodeUsage

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var clients []model.Client
		if err := tx.Where("origin_id > 0 AND (used_bytes > 0 OR up_bytes > 0 OR down_bytes > 0)").
			Find(&clients).Error; err != nil {
			return fmt.Errorf("read managed usage: %w", err)
		}
		if len(clients) == 0 {
			return nil
		}

		ids := make([]uint, 0, len(clients))
		for _, c := range clients {
			out = append(out, NodeUsage{
				OriginID: c.OriginID,
				Bytes:    c.UsedBytes,
				Up:       c.UpBytes,
				Down:     c.DownBytes,
			})
			ids = append(ids, c.ID)
		}

		return tx.Model(&model.Client{}).Where("id IN ?", ids).
			UpdateColumns(map[string]any{
				"used_bytes": 0, "up_bytes": 0, "down_bytes": 0,
				"updated_at": time.Now().UTC(),
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Hold applies what the panel decided: each named device, by its id on the
// panel, is held off here until the time given.
func (s *NodeSync) Hold(ctx context.Context, holds []NodeHold) (int, error) {
	if len(holds) == 0 || s.OnHold == nil {
		return 0, nil
	}
	origins := make([]uint, 0, len(holds))
	until := make(map[uint]time.Time, len(holds))
	for _, h := range holds {
		if h.OriginID == 0 {
			continue
		}
		origins = append(origins, h.OriginID)
		until[h.OriginID] = h.Until
	}
	var accounts []model.Account
	if err := s.db.WithContext(ctx).Where("origin_id IN ?", origins).Find(&accounts).Error; err != nil {
		return 0, fmt.Errorf("service: find held accounts: %w", err)
	}
	n := 0
	for _, a := range accounts {
		s.OnHold(ctx, a.ID, until[a.OriginID])
		n++
	}
	return n, nil
}

// Prune removes the managed tunnels the panel no longer sends: a tunnel
// deleted centrally must stop here too, device and all, or it keeps serving
// whoever still holds a file for it. keep lists the origin ids the panel
// still has on this node.
func (s *NodeSync) Prune(ctx context.Context, nodeID uint, keep []uint) (int, error) {
	var managed []model.Interface
	if err := s.db.WithContext(ctx).Where("node_id = ? AND managed = ?", nodeID, true).
		Find(&managed).Error; err != nil {
		return 0, fmt.Errorf("service: read managed interfaces: %w", err)
	}
	stay := make(map[uint]bool, len(keep))
	for _, id := range keep {
		stay[id] = true
	}
	removed := 0
	for i := range managed {
		iface := &managed[i]
		if stay[iface.OriginID] {
			continue
		}
		if s.OnRemoveInterface != nil {
			s.OnRemoveInterface(ctx, iface)
		}
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("interface_id = ?", iface.ID).Delete(&model.Account{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.Interface{}, iface.ID).Error; err != nil {
				return err
			}
			// A managed customer with nothing left here is gone from here.
			return tx.Where("origin_id > 0 AND NOT EXISTS (SELECT 1 FROM accounts WHERE accounts.client_id = clients.id)").
				Delete(&model.Client{}).Error
		})
		if err != nil {
			return removed, fmt.Errorf("service: remove withdrawn tunnel %s: %w", iface.Name, err)
		}
		s.log.Info("withdrawn tunnel removed", "interface", iface.Name)
		removed++
	}
	return removed, nil
}
