package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Removing a tunnel, or a whole server, customers and all, in one step.

func (p seatPanel) ifaces(t *testing.T) *Interfaces {
	t.Helper()
	return NewInterfaces(p.db, p.clients.pools, quietLog())
}

// servers is which tunnels a customer is on, in order; nil when they are gone.
func (p seatPanel) servers(t *testing.T, id uint) []uint {
	t.Helper()
	var n int64
	p.db.Model(&model.Client{}).Where("id = ?", id).Count(&n)
	if n == 0 {
		return nil
	}
	var got []uint
	p.db.Model(&model.Account{}).Where("client_id = ?", id).Distinct().Order("interface_id").Pluck("interface_id", &got)
	return got
}

func (p seatPanel) on(t *testing.T, name string, servers ...uint) *model.Client {
	t.Helper()
	c, err := p.clients.Create(context.Background(), CreateInput{Name: name, InterfaceIDs: servers, DeviceLimit: 1})
	if err != nil {
		t.Fatalf("make %s: %v", name, err)
	}
	return c
}

func sameIDs(a, b []uint) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// A customer with another server keeps it; one on the tunnel alone is
// moved, or removed, as asked -- and asked first, never left with nothing.
func TestATunnelIsRemovedWithItsCustomersInOneStep(t *testing.T) {
	for _, alone := range []string{"move", "delete"} {
		p := newSeatPanel(t)
		ifs := p.ifaces(t)
		both := p.on(t, "both", 1, 2)
		only := p.on(t, "only", 1)
		other := p.on(t, "other", 2)

		res, err := RemoveTunnel(context.Background(), p.clients, ifs, 1, TunnelRemoval{DryRun: true})
		if err != nil {
			t.Fatalf("%s: preview: %v", alone, err)
		}
		if res.Customers != 2 || res.Others != 1 || res.Alone != 1 || len(res.AloneNames) != 1 || res.AloneNames[0] != "only" {
			t.Fatalf("%s: the preview says %+v", alone, res)
		}
		if _, err := RemoveTunnel(context.Background(), p.clients, ifs, 1, TunnelRemoval{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: removed with a customer on it alone and no plan for them: %v", alone, err)
		}
		if !sameIDs(p.servers(t, only.ID), []uint{1}) {
			t.Fatalf("%s: a refused removal moved someone", alone)
		}

		res, err = RemoveTunnel(context.Background(), p.clients, ifs, 1, TunnelRemoval{Alone: alone, MoveTo: 2})
		if err != nil {
			t.Fatalf("%s: %v", alone, err)
		}
		var left int64
		p.db.Model(&model.Interface{}).Where("id = 1").Count(&left)
		if !res.Removed || left != 0 {
			t.Fatalf("%s: the tunnel is still there", alone)
		}
		if !sameIDs(p.servers(t, both.ID), []uint{2}) || !sameIDs(p.servers(t, other.ID), []uint{2}) {
			t.Fatalf("%s: those with another server: %v, %v", alone, p.servers(t, both.ID), p.servers(t, other.ID))
		}
		switch alone {
		case "move":
			if !sameIDs(p.servers(t, only.ID), []uint{2}) || res.Moved != 1 {
				t.Fatalf("move: the one on it alone is on %v (moved %d)", p.servers(t, only.ID), res.Moved)
			}
		case "delete":
			if p.servers(t, only.ID) != nil || res.Deleted != 1 {
				t.Fatalf("delete: the one on it alone is still there (deleted %d)", res.Deleted)
			}
		}
		p.balanced(t)
	}
}

// Moved nowhere: not to the tunnel being removed, nor to nothing.
func TestATunnelsCustomersAreMovedSomewhereReal(t *testing.T) {
	p := newSeatPanel(t)
	p.on(t, "only", 1)
	for _, to := range []uint{0, 1, 99} {
		if _, err := RemoveTunnel(context.Background(), p.clients, p.ifaces(t), 1, TunnelRemoval{Alone: "move", MoveTo: to}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("moved to %d: %v", to, err)
		}
	}
}

// A server's tunnels go together, and "alone" means on nothing outside the
// server: a customer on two of its tunnels is not safe on the other.
func TestAServerIsEmptiedWithItsCustomersInOneStep(t *testing.T) {
	p := newSeatPanel(t)
	ctx := context.Background()
	node := model.Node{Name: "germany", Kind: model.KindRemote, Address: "https://de.example.com"}
	if err := p.db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	var de []uint
	for i, sub := range []string{"10.60.0.0/24", "10.61.0.0/24"} {
		iface := model.Interface{Name: fmt.Sprintf("de%d", i), Protocol: model.ProtocolWireGuard, ListenPort: 52000 + i,
			Subnet: sub, EndpointHost: "de.example.com", NodeID: node.ID}
		if err := p.db.Create(&iface).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := p.clients.pools.Add(iface.ID, sub); err != nil {
			t.Fatal(err)
		}
		de = append(de, iface.ID)
	}
	twoThere := p.on(t, "two-there", de[0], de[1])
	alsoHome := p.on(t, "also-home", de[0], 1)

	res, err := EmptyNode(ctx, p.clients, p.ifaces(t), node, TunnelRemoval{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Customers != 2 || res.Alone != 1 || res.AloneNames[0] != "two-there" || len(res.Tunnels) != 2 {
		t.Fatalf("the preview says %+v", res)
	}
	if _, err := EmptyNode(ctx, p.clients, p.ifaces(t), node, TunnelRemoval{Alone: "move", MoveTo: de[1]}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("moved onto the server being removed: %v", err)
	}
	res, err = EmptyNode(ctx, p.clients, p.ifaces(t), node, TunnelRemoval{Alone: "move", MoveTo: 2})
	if err != nil {
		t.Fatal(err)
	}
	var left int64
	p.db.Model(&model.Interface{}).Where("node_id = ?", node.ID).Count(&left)
	if !res.Emptied || left != 0 {
		t.Fatalf("%d of its tunnels are left", left)
	}
	if !sameIDs(p.servers(t, twoThere.ID), []uint{2}) || !sameIDs(p.servers(t, alsoHome.ID), []uint{1}) {
		t.Fatalf("after: %v and %v", p.servers(t, twoThere.ID), p.servers(t, alsoHome.ID))
	}
	p.balanced(t)
}

// Removed with its customers past the thousand one bulk action takes.
func TestManyCustomersAreRemovedTogether(t *testing.T) {
	p := newSeatPanel(t)
	rows := make([]model.Client, 1500)
	for i := range rows {
		rows[i] = model.Client{Name: fmt.Sprintf("c%d", i), Status: model.StatusActive}
	}
	if err := p.db.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]uint, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	if err := p.clients.DeleteCustomers(context.Background(), ids); err != nil {
		t.Fatalf("delete 1500: %v", err)
	}
	var n int64
	p.db.Model(&model.Client{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d left", n)
	}
}
