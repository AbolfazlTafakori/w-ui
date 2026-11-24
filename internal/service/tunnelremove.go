package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/idset"
)

// Removing a tunnel, customers and all, in one step.
//
// A tunnel with customers on it cannot be deleted: their files would point
// at nothing. Before, that meant taking everyone off it first -- one by one,
// or with an action few found -- and a customer on no other server was
// refused even that. Now one step does it all, after saying what it will do:
//
//   - a customer who has other servers keeps them and only loses this one;
//   - a customer on this tunnel alone is moved to another tunnel the
//     operator picks, or removed, as they choose -- never left with nothing;
//   - then the tunnel goes.
//
// The plan is read first (DryRun) so the dialog can say exactly who is
// affected; the run checks again, since the tunnel may have changed since.

// TunnelRemoval says what to do with the customers on this tunnel alone.
type TunnelRemoval struct {
	// Alone is "move" -- to MoveTo -- or "delete". Needed only when there
	// are customers on this tunnel and no other.
	Alone  string `json:"alone"`
	MoveTo uint   `json:"moveTo"`
	// DryRun says what would happen and changes nothing.
	DryRun bool `json:"dryRun"`
}

// TunnelRemovalResult is what a removal did, or would do.
type TunnelRemovalResult struct {
	Tunnel string `json:"tunnel"`
	// Customers is everyone on it; Others, those who keep another server;
	// Alone, those on this tunnel and no other.
	Customers int `json:"customers"`
	Others    int `json:"others"`
	Alone     int `json:"alone"`
	// AloneNames names the first of those on it alone, for the dialog.
	AloneNames []string `json:"aloneNames"`
	// Moved and Deleted are what happened to them; Removed, whether the
	// tunnel itself is gone.
	Moved   int  `json:"moved"`
	Deleted int  `json:"deleted"`
	Removed bool `json:"removed"`
}

// aloneShown is how many of the customers on a tunnel alone are named.
const aloneShown = 20

// RemoveTunnel takes everyone off a tunnel by plan and deletes it.
func RemoveTunnel(ctx context.Context, clients *Clients, ifaces *Interfaces, id uint, plan TunnelRemoval) (*TunnelRemovalResult, error) {
	return removeTunnel(ctx, clients, ifaces, id, plan, 0)
}

// removeTunnel is RemoveTunnel with the server being removed along with it,
// if any, which the customers cannot be moved to.
func removeTunnel(ctx context.Context, clients *Clients, ifaces *Interfaces, id uint, plan TunnelRemoval, leavingNode uint) (*TunnelRemovalResult, error) {
	var iface model.Interface
	if err := ifaces.db.WithContext(ctx).First(&iface, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no interface %d", ErrNotFound, id)
	}
	out := &TunnelRemovalResult{Tunnel: iface.Name, AloneNames: []string{}}

	others, alone, names, err := customersOnTunnels(ctx, ifaces, []uint{id})
	if err != nil {
		return nil, err
	}
	out.Customers, out.Others, out.Alone = len(others)+len(alone), len(others), len(alone)
	out.AloneNames = names

	// A preview with nothing chosen yet only counts: the dialog asks what
	// to do with those on it alone from what it says.
	if plan.DryRun && plan.Alone == "" {
		return out, nil
	}
	if len(alone) > 0 {
		switch plan.Alone {
		case "move":
			if err := checkMoveTo(ctx, ifaces, id, plan.MoveTo, leavingNode); err != nil {
				return nil, err
			}
		case "delete":
		default:
			return out, invalidField("alone", "%d customer(s) are on %s and no other server: move them to another tunnel, or remove them",
				len(alone), iface.Name)
		}
	}
	if plan.DryRun {
		return out, nil
	}

	// Those on it alone first, so taking everyone off it leaves nobody
	// without a server.
	if len(alone) > 0 && plan.Alone == "move" {
		res, err := clients.AttachServers(ctx, alone, []uint{plan.MoveTo})
		if err != nil {
			return out, err
		}
		if err := bulkFailures("moving them to the other tunnel", res); err != nil {
			return out, err
		}
		out.Moved = res.Changed + res.Unchanged
	}
	if len(alone) > 0 && plan.Alone == "delete" {
		if err := clients.DeleteCustomers(ctx, alone); err != nil {
			return out, err
		}
		out.Deleted = len(alone)
	}
	everyone := append(append([]uint(nil), others...), alone...)
	if plan.Alone == "delete" {
		everyone = others
	}
	if len(everyone) > 0 {
		res, err := clients.DetachServers(ctx, everyone, []uint{id})
		if err != nil {
			return out, err
		}
		if err := bulkFailures("taking them off "+iface.Name, res); err != nil {
			return out, err
		}
	}
	if err := ifaces.Delete(ctx, id); err != nil {
		return out, err
	}
	out.Removed = true
	ifaces.log.Warn("tunnel removed with its customers moved", "interface", iface.Name,
		"customers", out.Customers, "keptOtherServers", out.Others, "moved", out.Moved, "deleted", out.Deleted)
	return out, nil
}

// customersOnTunnels is who is on any of the tunnels: those who also have a
// server outside them, and those on them alone (with the first of their
// names). For a server being removed the tunnels are all of its own, so a
// customer on two of them is alone there, not safe on the other.
func customersOnTunnels(ctx context.Context, ifaces *Interfaces, tunnels []uint) (others, alone []uint, names []string, err error) {
	db := ifaces.db.WithContext(ctx)
	var ids []uint
	if err := db.Model(&model.Account{}).Where("interface_id IN ?", tunnels).
		Distinct().Pluck("client_id", &ids).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("service: find the customers on them: %w", err)
	}
	elsewhere := map[uint]bool{}
	for _, part := range idset.Chunks(ids, idset.Batch) {
		var got []uint
		if err := db.Model(&model.Account{}).Where("client_id IN ? AND interface_id NOT IN ?", part, tunnels).
			Distinct().Pluck("client_id", &got).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("service: find customers' other servers: %w", err)
		}
		for _, c := range got {
			elsewhere[c] = true
		}
	}
	for _, c := range ids {
		if elsewhere[c] {
			others = append(others, c)
		} else {
			alone = append(alone, c)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
	sort.Slice(alone, func(i, j int) bool { return alone[i] < alone[j] })
	names = []string{}
	if len(alone) > 0 {
		if err := db.Model(&model.Client{}).Where("id IN ?", alone[:min(len(alone), aloneShown)]).
			Order("id").Pluck("name", &names).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("service: name the customers: %w", err)
		}
	}
	return others, alone, names, nil
}

// checkMoveTo refuses a tunnel the customers cannot be moved to: missing,
// the one being removed, one on the server being removed with it (leaving,
// zero for none), or an inbound of a 3x-ui or Pasarguard node.
func checkMoveTo(ctx context.Context, ifaces *Interfaces, from, to, leaving uint) error {
	if to == 0 || to == from {
		return invalidField("moveTo", "choose the tunnel to move them to")
	}
	var dest model.Interface
	if err := ifaces.db.WithContext(ctx).First(&dest, to).Error; err != nil {
		return invalidField("moveTo", "there is no tunnel %d to move them to", to)
	}
	if leaving != 0 && dest.NodeID == leaving {
		return invalidField("moveTo", "%s is on the server being removed; choose a tunnel on another", dest.Name)
	}
	if dest.Protocol == model.ProtocolXray {
		return invalidField("moveTo", "%s is an inbound of another panel; choose one of this panel's tunnels", dest.Name)
	}
	return nil
}

// bulkFailures turns the customers a bulk server move refused into one
// error that names them.
func bulkFailures(doing string, res *ServerBulkResult) error {
	if res == nil || len(res.Failures) == 0 {
		return nil
	}
	parts := make([]string, 0, len(res.Failures))
	for name, why := range res.Failures {
		parts = append(parts, name+": "+why)
	}
	sort.Strings(parts)
	if len(parts) > 5 {
		parts = append(parts[:5], fmt.Sprintf("and %d more", len(res.Failures)-5))
	}
	return fmt.Errorf("%w: %s, %d customer(s) were refused -- %s", ErrInvalid, doing, len(res.Failures), strings.Join(parts, "; "))
}
