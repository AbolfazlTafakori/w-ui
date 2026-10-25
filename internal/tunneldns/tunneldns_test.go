package tunneldns

import (
	"net/netip"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestCustomersAreHandedTheTunnelsResolver(t *testing.T) {
	defer SetProxy(false)
	defer local.Store(nil)
	iface := &model.Interface{Subnet: "10.66.0.0/24", DNS: "1.1.1.1"}
	if got := For(iface); got != "1.1.1.1" {
		t.Fatalf("with the proxy off: %q", got)
	}
	SetProxy(true)
	if got := For(iface); got != "10.66.0.1" {
		t.Fatalf("with the proxy on: %q", got)
	}
	if got := For(&model.Interface{Subnet: "not a subnet", DNS: "8.8.8.8"}); got != "8.8.8.8" {
		t.Fatalf("a tunnel with no subnet to answer on: %q", got)
	}
}

// A gateway on this server where the resolver could not open its port is
// not handed out: the customer would have no resolver at all.
func TestAGatewayNobodyAnswersOnIsNotHandedOut(t *testing.T) {
	defer SetProxy(false)
	defer local.Store(nil)
	SetProxy(true)
	gw := netip.MustParseAddr("10.66.0.1")
	other := netip.MustParseAddr("10.70.0.1")
	SetAnswering([]netip.Addr{gw, other}, []netip.Addr{other})
	if got := For(&model.Interface{Subnet: "10.66.0.0/24", DNS: "1.1.1.1"}); got != "1.1.1.1" {
		t.Fatalf("a gateway the resolver is not on: %q", got)
	}
	if got := For(&model.Interface{Subnet: "10.70.0.0/24", DNS: "1.1.1.1"}); got != "10.70.0.1" {
		t.Fatalf("a gateway it answers on: %q", got)
	}
	// A node's tunnel, or one not up here yet: handed out as before.
	if got := For(&model.Interface{Subnet: "10.80.0.0/24", DNS: "1.1.1.1"}); got != "10.80.0.1" {
		t.Fatalf("a node's tunnel: %q", got)
	}
}
