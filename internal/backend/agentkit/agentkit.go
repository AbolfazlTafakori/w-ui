// Package agentkit is what the drivers of the tunnels the root agent runs
// share: L2TP and PPTP, IKEv2, OpenConnect. Each driver tells the agent what
// its tunnel is in a state file, reads back the agent's report, gives the
// tunnel a device holding its gateway, and hands customers the tunnel's
// servers. That is the same for all of them, and is here, so no driver
// depends on another and a change to one protocol cannot break the others.
package agentkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// ReportStale is how old the agent's report may be before the agent is
// taken as not running; StartGrace is how long it is given to answer at
// first, while it installs what a tunnel needs.
const (
	ReportStale = 20 * time.Second
	StartGrace  = 15 * time.Minute
)

// ErrNoAgent is the agent not writing its report.
var ErrNoAgent = errors.New("the root agent that runs L2TP, PPTP, IKEv2 and OpenConnect tunnels (wui-l2tp) is not answering")

// Gateway is the first host of a subnet: the server's end of every session.
func Gateway(subnet string) (netip.Addr, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil || !p.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("subnet %q is not an IPv4 network", subnet)
	}
	return p.Masked().Addr().Next(), nil
}

// Resolvers is the IPv4 addresses in a comma-separated list, two at most:
// what a session can be handed as its resolvers.
func Resolvers(field string) []string {
	var out []string
	for _, part := range strings.Split(field, ",") {
		a, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || !a.Is4() {
			continue
		}
		out = append(out, a.String())
		if len(out) == 2 {
			break
		}
	}
	return out
}

// WriteAtomic writes through a temporary file renamed into place, so the
// agent never reads half a state.
func WriteAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wui-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadReport is the agent's report as it is.
func ReadReport(p l2tpconf.Paths) (l2tpconf.Report, error) {
	var r l2tpconf.Report
	raw, err := os.ReadFile(p.Report())
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("the agent's report: %w", err)
	}
	return r, nil
}

// FreshReport is the agent's report while the agent is still writing it.
func FreshReport(p l2tpconf.Paths) (l2tpconf.Report, error) {
	r, err := ReadReport(p)
	if err != nil {
		return l2tpconf.Report{}, err
	}
	if time.Since(time.Unix(r.At, 0)) > ReportStale {
		return l2tpconf.Report{}, ErrNoAgent
	}
	return r, nil
}
