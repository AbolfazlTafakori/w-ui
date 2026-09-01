// Package wgdriver drives WireGuard and AmneziaWG interfaces.
//
// It is the half of the panel that touches the kernel's tunnel state. The
// reconciler hands it the complete set of accounts that should exist and the
// driver makes the interface match — adding, updating and removing peers in one
// operation rather than reacting to individual events.
//
// Two transports live behind one driver. A standard WireGuard interface is
// configured over netlink, which is fast, structured and needs no subprocess.
// An AmneziaWG interface cannot be: its obfuscation parameters are unknown to
// the WireGuard netlink API, so those are applied through the `awg` tool with a
// generated config instead.
package wgdriver

import (
	"errors"
	"fmt"
	"net"
	"sort"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Errors specific to this driver.
var (
	ErrNoTool      = errors.New("wgdriver: required tool not installed")
	ErrLinkFailed  = errors.New("wgdriver: could not prepare the interface")
	ErrUnsupported = errors.New("wgdriver: WireGuard is only available on Linux")
)

// Register makes the driver available to the backend registry.
func Register() {
	backend.Register(model.ProtocolWireGuard, func() backend.Backend { return New() })
}

// peerSet is the desired peers keyed by public key, which is how WireGuard
// itself identifies them.
type peerSet map[string]backend.DesiredAccount

func toPeerSet(accounts []backend.DesiredAccount) peerSet {
	out := make(peerSet, len(accounts))
	for _, a := range accounts {
		if a.PublicKey == "" {
			continue // an account with no key cannot be a peer
		}
		out[a.PublicKey] = a
	}
	return out
}

// diff works out what has to change for the interface to match `want`.
//
// The comparison is by public key because that is the peer's identity to the
// kernel. A device whose key was rotated therefore reads as one removal and one
// addition, which is exactly right: the old key must stop working the moment
// the new one is issued.
type diff struct {
	add    []backend.DesiredAccount
	update []backend.DesiredAccount
	remove []string // public keys
}

func computeDiff(want peerSet, have map[string]string) diff {
	var d diff

	for key, acc := range want {
		currentIP, exists := have[key]
		switch {
		case !exists:
			d.add = append(d.add, acc)
		case currentIP != acc.IP.String():
			// The same key on a different address: the allowed-ips filter has
			// to move with it or the peer can no longer send.
			d.update = append(d.update, acc)
		}
	}

	for key := range have {
		if _, keep := want[key]; !keep {
			d.remove = append(d.remove, key)
		}
	}

	// Sorted so a report and any logging read the same way every run.
	sort.Slice(d.add, func(i, j int) bool { return d.add[i].ID < d.add[j].ID })
	sort.Slice(d.update, func(i, j int) bool { return d.update[i].ID < d.update[j].ID })
	sort.Strings(d.remove)
	return d
}

func (d diff) report(unchanged int) backend.SyncReport {
	return backend.SyncReport{
		Added:     len(d.add),
		Removed:   len(d.remove),
		Updated:   len(d.update),
		Unchanged: unchanged,
	}
}

// toolFor returns the command a given interface mode is driven with.
//
// AmneziaWG ships a fork of wireguard-tools under its own name; using `wg` on
// an AmneziaWG interface silently drops every obfuscation parameter, producing
// a tunnel that looks configured and is trivially fingerprinted.
func toolFor(iface *model.Interface) string {
	if iface.Mode == model.ModeAmnezia {
		return "awg"
	}
	return "wg"
}

// linkType is the kernel link type behind an interface mode.
func linkType(iface *model.Interface) string {
	if iface.Mode == model.ModeAmnezia {
		return "amneziawg"
	}
	return "wireguard"
}

// allowedIP renders the single-address filter for a peer.
func allowedIP(a backend.DesiredAccount) string {
	return fmt.Sprintf("%s/32", a.IP.String())
}

// singleHost is the peer's address as a /32 network.
func singleHost(a backend.DesiredAccount) net.IPNet {
	return net.IPNet{IP: a.IP.AsSlice(), Mask: net.CIDRMask(32, 32)}
}

// peerConfigs turns a diff into the exact set of peer operations to send.
//
// Only what actually changed is sent, and the caller must not ask the kernel to
// replace the whole peer list. WireGuard holds session state per peer: removing
// and re-adding an unchanged peer throws away its live handshake. Doing that on
// every reconcile tick leaves every customer permanently renegotiating and never
// passing traffic — the tunnel looks configured from both ends and moves nothing.
func peerConfigs(d diff) []wgtypes.PeerConfig {
	out := make([]wgtypes.PeerConfig, 0, len(d.add)+len(d.update)+len(d.remove))

	for _, a := range append(append([]backend.DesiredAccount{}, d.add...), d.update...) {
		pub, err := wgtypes.ParseKey(a.PublicKey)
		if err != nil {
			continue // reported by the caller; one bad key must not fail the batch
		}

		pc := wgtypes.PeerConfig{
			PublicKey: pub,
			// Scoped to this peer, so it moves an address without disturbing
			// anything else about the session.
			ReplaceAllowedIPs: true,
			AllowedIPs:        []net.IPNet{singleHost(a)},
		}
		if a.PresharedKey != "" {
			if psk, err := wgtypes.ParseKey(a.PresharedKey); err == nil {
				pc.PresharedKey = &psk
			}
		}
		out = append(out, pc)
	}

	for _, key := range d.remove {
		pub, err := wgtypes.ParseKey(key)
		if err != nil {
			continue
		}
		out = append(out, wgtypes.PeerConfig{PublicKey: pub, Remove: true})
	}

	return out
}
