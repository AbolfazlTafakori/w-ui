// Package tunneldns says which resolver a tunnel's customers are handed.
//
// While the panel's own resolver is on, it is the tunnel's gateway -- the
// server's end of every customer's tunnel, where the resolver answers --
// unless the gateway is up on this server and the resolver still could not
// open its port there: the customer would be given a resolver that is not
// there, and resolve nothing. They get the tunnel's own setting instead.
// When the resolver is off, the tunnel's setting alone. Every file a
// customer is given, and every tunnel an agent runs, asks here, so the
// answer is the same everywhere. The engine is its only writer (SetProxy,
// SetAnswering).
package tunneldns

import (
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/abolfazl/w-ui/internal/database/model"
)

var proxyOn atomic.Bool

// here is where the resolver was asked to answer on this server, and where
// it does.
type here struct {
	wanted, answering map[netip.Addr]bool
}

var local atomic.Pointer[here]

// SetProxy says whether the panel's own resolver is on.
func SetProxy(on bool) { proxyOn.Store(on) }

// SetAnswering says which of this server's tunnel gateways are up and the
// resolver was asked to answer on, and which it answers on. A gateway not up
// yet -- a tunnel still coming up -- is left out of wanted: it is handed out,
// and answered on once it is.
func SetAnswering(wanted, answering []netip.Addr) {
	h := &here{wanted: map[netip.Addr]bool{}, answering: map[netip.Addr]bool{}}
	for _, a := range wanted {
		h.wanted[a] = true
	}
	for _, a := range answering {
		h.answering[a] = true
	}
	local.Store(h)
}

// For is the resolver iface's customers are handed.
func For(iface *model.Interface) string {
	if !proxyOn.Load() {
		return iface.DNS
	}
	pfx, err := netip.ParsePrefix(strings.TrimSpace(iface.Subnet))
	if err != nil {
		return iface.DNS
	}
	gw := pfx.Masked().Addr().Next()
	if h := local.Load(); h != nil && h.wanted[gw] && !h.answering[gw] {
		return iface.DNS
	}
	return gw.String()
}
