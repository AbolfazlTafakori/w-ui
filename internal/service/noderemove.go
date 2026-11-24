package service

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Removing a server's tunnels, customers and all, so the server itself can
// be removed: the same rules as one tunnel (RemoveTunnel), over all of its
// tunnels at once. "On no other server" means outside this one: a customer
// on two of its tunnels has nowhere left when both go.
//
// The node entry itself is removed by the caller (fleet.Nodes.Delete), which
// this package cannot reach; the caller also tells a W-UI node to take its
// managed tunnels down, so nothing keeps serving there with nobody managing it.

// WithdrawFromNode tells a W-UI node to take down every tunnel this panel
// gave it; nothing for another kind, whose inbounds stay that panel's.
func (s *Clients) WithdrawFromNode(ctx context.Context, node model.Node) error {
	if node.Kind != model.KindRemote || s.remote == nil {
		return nil
	}
	return s.remote.Withdraw(ctx, node)
}

// NodeRemovalResult is what removing a server's tunnels did, or would do.
type NodeRemovalResult struct {
	Node string `json:"node"`
	// Tunnels names the server's tunnels.
	Tunnels []string `json:"tunnels"`
	// Customers, Others and Alone are as for one tunnel, over all of them.
	Customers  int      `json:"customers"`
	Others     int      `json:"others"`
	Alone      int      `json:"alone"`
	AloneNames []string `json:"aloneNames"`
	Moved      int      `json:"moved"`
	Deleted    int      `json:"deleted"`
	// Emptied is whether every tunnel is gone and the server may go.
	Emptied bool `json:"emptied"`
}

// EmptyNode takes everyone off a server's tunnels by plan and deletes them,
// a 3x-ui or Pasarguard node's inbounds excepted: those go with the node.
func EmptyNode(ctx context.Context, clients *Clients, ifaces *Interfaces, node model.Node, plan TunnelRemoval) (*NodeRemovalResult, error) {
	var tunnels []model.Interface
	if err := ifaces.db.WithContext(ctx).Where("node_id = ?", node.ID).Order("id").Find(&tunnels).Error; err != nil {
		return nil, fmt.Errorf("service: read the server's tunnels: %w", err)
	}
	out := &NodeRemovalResult{Node: node.Name, Tunnels: []string{}, AloneNames: []string{}}
	ids := make([]uint, 0, len(tunnels))
	for _, t := range tunnels {
		ids = append(ids, t.ID)
		out.Tunnels = append(out.Tunnels, t.Name)
	}
	if len(ids) == 0 {
		out.Emptied = true
		return out, nil
	}
	others, alone, names, err := customersOnTunnels(ctx, ifaces, ids)
	if err != nil {
		return nil, err
	}
	out.Customers, out.Others, out.Alone, out.AloneNames = len(others)+len(alone), len(others), len(alone), names

	// A preview with nothing chosen yet only counts: the dialog asks what
	// to do with those on it alone from what it says.
	if plan.DryRun && plan.Alone == "" {
		return out, nil
	}
	if len(alone) > 0 {
		switch plan.Alone {
		case "move":
			if err := checkMoveTo(ctx, ifaces, 0, plan.MoveTo, node.ID); err != nil {
				return nil, err
			}
		case "delete":
		default:
			return out, invalidField("alone", "%d customer(s) are on %s and no other server: move them to a tunnel on another, or remove them",
				len(alone), node.Name)
		}
	}
	if plan.DryRun {
		return out, nil
	}

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
	// Everyone left has a server outside this one now, so each tunnel can be
	// emptied and removed on its own.
	for _, t := range tunnels {
		on, err := ifaces.CustomersOn(ctx, t.ID)
		if err != nil {
			return out, err
		}
		if len(on) > 0 {
			res, err := clients.DetachServers(ctx, on, []uint{t.ID})
			if err != nil {
				return out, err
			}
			if err := bulkFailures("taking them off "+t.Name, res); err != nil {
				return out, err
			}
		}
		if t.Protocol == model.ProtocolXray {
			continue // an inbound of that panel's: removed with the node
		}
		if err := ifaces.Delete(ctx, t.ID); err != nil {
			return out, err
		}
	}
	out.Emptied = true
	ifaces.log.Warn("a server's tunnels removed with their customers moved", "node", node.Name,
		"tunnels", len(tunnels), "customers", out.Customers, "keptOtherServers", out.Others,
		"moved", out.Moved, "deleted", out.Deleted)
	return out, nil
}
