package tunneldns

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestCustomersAreHandedTheTunnelsResolver(t *testing.T) {
	defer SetProxy(false)
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
