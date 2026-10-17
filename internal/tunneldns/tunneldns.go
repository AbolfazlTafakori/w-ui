// Package tunneldns says which resolver a tunnel's customers are handed.
//
// While the panel's own resolver is on, it is the tunnel's gateway -- the
// server's end of every customer's tunnel, where the resolver answers; when
// it is off, whatever the tunnel's settings say. Every file a customer is
// given, and every tunnel an agent runs, asks here, so the answer is the same
// everywhere. The engine settings are its only writer (SetProxy).
package tunneldns

import (
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/abolfazl/w-ui/internal/database/model"
)

var proxyOn atomic.Bool

// SetProxy says whether the panel's own resolver is on.
func SetProxy(on bool) { proxyOn.Store(on) }

// For is the resolver iface's customers are handed.
func For(iface *model.Interface) string {
	if proxyOn.Load() {
		if pfx, err := netip.ParsePrefix(strings.TrimSpace(iface.Subnet)); err == nil {
			return pfx.Masked().Addr().Next().String()
		}
	}
	return iface.DNS
}
