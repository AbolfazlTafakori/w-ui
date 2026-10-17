package nodes

import (
	"context"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Remote is how the service speaks to the 3x-ui and Pasarguard panels used
// as nodes (service.RemotePanels).
type Remote struct{}

// CreateXrayInbound makes an inbound on the node (CreateXrayInbound).
func (Remote) CreateXrayInbound(ctx context.Context, node model.Node, in service.XrayInboundInput) (model.XUIInbound, error) {
	return CreateXrayInbound(ctx, node, in)
}

// ImportCandidates reads the node's own users (ImportCandidates).
func (Remote) ImportCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]service.ImportCandidate, error) {
	return ImportCandidates(ctx, node, tunnels)
}

var _ service.RemotePanels = Remote{}
