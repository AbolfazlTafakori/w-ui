package nodes

import (
	"context"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/service"
)

// rig is this panel with one 3x-ui or Pasarguard node: its database, the
// services the rounds and the operator use, and what the rounds charged.
type rig struct {
	t       *testing.T
	ctx     context.Context
	db      *gorm.DB
	s       *Syncer
	clients *service.Clients
	node    model.Node

	mu      sync.Mutex
	charged map[uint]uint64 // customer -> bytes charged
}

func newRig(t *testing.T, node model.Node) *rig {
	t.Helper()
	db := testDB(t)
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	// A node on this machine, which is where the fakes listen.
	node.AllowPrivateAddress = true
	if node.Name == "" {
		node.Name = "panel"
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	pools := ipam.NewPools()
	r := &rig{t: t, ctx: context.Background(), db: db, node: node, charged: map[uint]uint64{},
		clients: service.NewClients(db, pools, quietLog())}
	r.s = NewSyncer(db, func(u []service.NodeUsage, _ []service.NodeDeviceUsage) {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, x := range u {
			r.charged[x.OriginID] += x.Bytes
		}
	}, quietLog())
	r.s.XUIMirror = service.NewInterfaces(db, pools, quietLog()).MirrorXUI
	// The sign-ins and versions kept between rounds are kept by node; a
	// test's node is new, but its id is not.
	t.Cleanup(func() {
		xuiSessions.Delete(xuiSessionKey(node))
		xuiInfos.Delete(xuiInfoKey(node))
		pgSessions.Delete(pgSessionKey(node))
	})
	prev := service.ImportCandidates
	service.ImportCandidates = ImportCandidates
	t.Cleanup(func() { service.ImportCandidates = prev })
	return r
}

// round runs one round against the node, as the loop does, and fails the
// test on an error it reports.
func (r *rig) round() {
	r.t.Helper()
	if err := r.roundErr(); err != nil {
		r.t.Fatalf("round: %v", err)
	}
}

func (r *rig) roundErr() error {
	var node model.Node
	if err := r.db.First(&node, r.node.ID).Error; err != nil {
		r.t.Fatal(err)
	}
	if node.Kind == model.KindPG {
		return r.s.pgRound(r.ctx, node)
	}
	return r.s.xuiRound(r.ctx, node)
}

// tunnel is the tunnel held here for an inbound there, by its name.
func (r *rig) tunnel(name string) model.Interface {
	r.t.Helper()
	var f model.Interface
	if err := r.db.Where("node_id = ? AND name = ?", r.node.ID, name).First(&f).Error; err != nil {
		r.t.Fatalf("no tunnel %q: %v", name, err)
	}
	return f
}

// customer sells a plan of one on the tunnels named.
func (r *rig) customer(name string, tunnels ...string) *model.Client {
	r.t.Helper()
	in := service.CreateInput{Name: name, QuotaBytes: 10 << 30, DeviceLimit: 1}
	for _, n := range tunnels {
		in.InterfaceIDs = append(in.InterfaceIDs, r.tunnel(n).ID)
	}
	c, err := r.clients.Create(r.ctx, in)
	if err != nil {
		r.t.Fatalf("customer %s: %v", name, err)
	}
	return c
}

// account is a customer's file on a tunnel, as it stands now.
func (r *rig) account(clientID uint, tunnel string) model.Account {
	r.t.Helper()
	var a model.Account
	if err := r.db.Where("client_id = ? AND interface_id = ?", clientID, r.tunnel(tunnel).ID).First(&a).Error; err != nil {
		r.t.Fatalf("no file of %d on %s: %v", clientID, tunnel, err)
	}
	return a
}

func (r *rig) setStatus(clientID uint, st model.ClientStatus) {
	r.t.Helper()
	if err := r.db.Model(&model.Client{}).Where("id = ?", clientID).Update("status", st).Error; err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) chargedTo(clientID uint) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.charged[clientID]
}

func (r *rig) tag() string {
	r.t.Helper()
	tag, err := service.PanelTag(r.ctx, r.db)
	if err != nil {
		r.t.Fatal(err)
	}
	return tag
}
