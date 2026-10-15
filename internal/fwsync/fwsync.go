// Package fwsync opens the tunnels' ports in the server's own firewall, ufw
// or firewalld, when one is on.
//
// The panel's own nftables tables let a tunnel's traffic through, but a
// firewall with a drop policy drops it in a table of its own, and nothing
// the panel adds can overrule that. A tunnel made on such a server was
// created, reported as working, and reachable by nobody. The panel runs
// unprivileged and cannot change the firewall, so it writes the ports its
// tunnels need to a file, and a root helper started on every change of it
// (wui-firewall.path) opens them.
//
// Only what the helper opened itself is ever closed. A rule that was there
// before -- the operator's own -- is left alone when the tunnel goes, and
// the record of what is the helper's is kept where only root can write: a
// panel that could write it could have root delete any rule, SSH's
// included.
package fwsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// DesiredFile is where, under the panel's data directory, the ports are
// written.
const DesiredFile = "firewall/ports.json"

// maxRules is far past what a server's tunnels need; a file with more is
// not one the panel wrote.
const maxRules = 256

// Rule is one thing to let in: a port on TCP or UDP, or a whole protocol --
// GRE for PPTP's data, ESP for IPsec's.
type Rule struct {
	Proto string `json:"proto"`
	Port  int    `json:"port,omitempty"`
}

func (r Rule) valid() bool {
	switch r.Proto {
	case "tcp", "udp":
		return r.Port >= 1 && r.Port <= 65535
	case "gre", "esp":
		return r.Port == 0
	}
	return false
}

func (r Rule) String() string {
	if r.Port == 0 {
		return r.Proto
	}
	return fmt.Sprintf("%d/%s", r.Port, r.Proto)
}

type file struct {
	Version int    `json:"version"`
	Open    []Rule `json:"open"`
}

// ForInterfaces is what a server's tunnels need let in. A tunnel on a port
// of its own (an L2TP, PPTP or IKEv2 one) is redirected to its daemon's
// standard port before the firewall sees it, so the standard port is the
// one opened.
func ForInterfaces(ifaces []model.Interface) []Rule {
	var out []Rule
	add := func(r ...Rule) { out = append(out, r...) }
	ipsec := []Rule{{Proto: "udp", Port: l2tpconf.PortIKE}, {Proto: "udp", Port: l2tpconf.PortNATT}, {Proto: "esp"}}
	for _, i := range ifaces {
		if !i.Enabled {
			continue
		}
		switch i.Protocol {
		case model.ProtocolWireGuard:
			add(Rule{"udp", i.ListenPort})
		case model.ProtocolOpenVPN:
			if i.OpenVPN.V.Transport == "tcp" {
				add(Rule{"tcp", i.ListenPort})
			} else {
				add(Rule{"udp", i.ListenPort})
			}
		case model.ProtocolOpenConnect:
			add(Rule{"tcp", i.ListenPort})
			if i.OpenConnect.V.DTLS {
				add(Rule{"udp", i.ListenPort})
			}
		case model.ProtocolL2TP:
			add(ipsec...)
			if i.L2TP.V.AllowPlain {
				add(Rule{"udp", l2tpconf.PortL2TP})
			}
		case model.ProtocolIKEv2:
			add(ipsec...)
		case model.ProtocolPPTP:
			add(Rule{"tcp", l2tpconf.PortPPTP}, Rule{Proto: "gre"})
		}
	}
	return normalize(out)
}

// normalize drops what is not a rule and what is there twice, and sorts, so
// the file only changes when the ports do.
func normalize(in []Rule) []Rule {
	seen := map[Rule]bool{}
	out := []Rule{}
	for _, r := range in {
		if r.valid() && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		return out[i].Port < out[j].Port
	})
	return out
}

// Encode is the file the panel writes.
func Encode(rules []Rule) []byte {
	b, _ := json.MarshalIndent(file{Version: 1, Open: normalize(rules)}, "", "  ")
	return append(b, '\n')
}

var errBadFile = errors.New("fwsync: not a ports file the panel writes")

// Decode reads it, refusing anything the panel would not have written.
func Decode(b []byte) ([]Rule, error) {
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", errBadFile, err)
	}
	if f.Version != 1 || len(f.Open) > maxRules {
		return nil, errBadFile
	}
	for _, r := range f.Open {
		if !r.valid() {
			return nil, fmt.Errorf("%w: %+v", errBadFile, r)
		}
	}
	return normalize(f.Open), nil
}
