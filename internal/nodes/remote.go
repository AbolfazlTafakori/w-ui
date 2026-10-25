package nodes

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/handover"
	"github.com/abolfazl/w-ui/internal/service"
)

// Remote is how the service speaks to the panels used as nodes
// (service.RemotePanels). DB is this panel's, for what a W-UI node is
// called with: its certificate rules and this panel's client certificate.
type Remote struct{ DB *gorm.DB }

// CreateXrayInbound makes an inbound on the node (CreateXrayInbound).
func (Remote) CreateXrayInbound(ctx context.Context, node model.Node, in service.XrayInboundInput) (model.XUIInbound, error) {
	return CreateXrayInbound(ctx, node, in)
}

// ImportCandidates reads the node's own users (ImportCandidates).
func (Remote) ImportCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]service.ImportCandidate, error) {
	return ImportCandidates(ctx, node, tunnels)
}

// Handover reads what a W-UI node has of its own.
func (r Remote) Handover(ctx context.Context, node model.Node) (handover.Handover, error) {
	var h handover.Handover
	if err := postNode(ctx, r.DB, node, "/api/node/handover", nil, &h); err != nil {
		return h, handoverErr(node, err)
	}
	return h, nil
}

// ClaimHandover tells a W-UI node which of its rows are now this panel's.
func (r Remote) ClaimHandover(ctx context.Context, node model.Node, c handover.Claim) error {
	return handoverErr(node, postNode(ctx, r.DB, node, "/api/node/handover/claim", c, nil))
}

// handoverErr names the node in what went wrong, and what to do about a
// panel that predates handing over.
func handoverErr(node model.Node, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errNoEndpoint):
		return fmt.Errorf("%w: %s cannot hand its customers over until its panel is updated to this version", service.ErrInvalid, node.Name)
	}
	return fmt.Errorf("%w: %s: %v", service.ErrInvalid, node.Name, err)
}

var _ service.RemotePanels = Remote{}
