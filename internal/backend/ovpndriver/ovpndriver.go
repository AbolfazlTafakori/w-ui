// Package ovpndriver drives an OpenVPN server process.
//
// It differs from the WireGuard driver in what "adding an account" means.
// WireGuard peers live in the kernel and are changed over netlink. OpenVPN keeps
// its account set in files that the running process re-reads on demand, so this
// driver's job is to keep those files correct and to disconnect sessions that
// should no longer exist.
//
// Nothing here restarts the server to add or remove a customer. A restart drops
// every other customer on the interface, and none of the files involved need it:
// the credential file is read on each login attempt and the address directory on
// each connection.
package ovpndriver

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
)

// Errors specific to this driver.
var (
	ErrNoBinary    = errors.New("ovpndriver: openvpn is not installed")
	ErrStartFailed = errors.New("ovpndriver: the server process would not start")
	ErrUnsupported = errors.New("ovpndriver: OpenVPN is only available on Linux")
	ErrNoTun       = errors.New("ovpndriver: /dev/net/tun is missing; load the tun module")
)

// Register makes the driver available to the backend registry.
func Register() {
	backend.Register(model.ProtocolOpenVPN, func() backend.Backend { return New() })
}

// accountSet is the desired accounts keyed by username, which is how OpenVPN
// identifies them once `username-as-common-name` is in effect.
type accountSet map[string]backend.DesiredAccount

func toAccountSet(accounts []backend.DesiredAccount) accountSet {
	out := make(accountSet, len(accounts))
	for _, a := range accounts {
		// A WireGuard account carries no username. Letting it through would
		// write a blank credential line that matches an empty login.
		if a.Username == "" || a.Secret == "" {
			continue
		}
		out[a.Username] = a
	}
	return out
}

// toRenderAccounts projects the desired set into what the renderer takes,
// in a stable order.
func toRenderAccounts(want accountSet) []ovpnconf.Account {
	out := make([]ovpnconf.Account, 0, len(want))
	for _, a := range want {
		out = append(out, ovpnconf.Account{
			Username: a.Username,
			Secret:   a.Secret,
			IP:       a.IP.String(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// diff is what has to change for the server to hold exactly the desired set.
//
// `have` is the address currently pinned to each username, read back from the
// address directory. Comparing against files rather than against a remembered
// value is what makes the driver self-healing: if someone edits the directory by
// hand, the next sync puts it back.
type diff struct {
	write  []ovpnconf.Account // new or changed address assignments
	remove []string           // usernames whose files should go
}

func computeDiff(want accountSet, have map[string]string) diff {
	var d diff

	for username, acc := range want {
		currentIP, exists := have[username]
		if !exists || currentIP != acc.IP.String() {
			d.write = append(d.write, ovpnconf.Account{
				Username: acc.Username,
				Secret:   acc.Secret,
				IP:       acc.IP.String(),
			})
		}
	}

	for username := range have {
		if _, keep := want[username]; !keep {
			d.remove = append(d.remove, username)
		}
	}

	sort.Slice(d.write, func(i, j int) bool { return d.write[i].Username < d.write[j].Username })
	sort.Strings(d.remove)
	return d
}

func (d diff) report(unchanged, added int) backend.SyncReport {
	return backend.SyncReport{
		Added:     added,
		Updated:   len(d.write) - added,
		Removed:   len(d.remove),
		Unchanged: unchanged,
	}
}

// session is one connected client as the management interface reports it.
type session struct {
	Username  string
	RealIP    string // public address the customer connects from
	VirtualIP string
	RX        uint64
	TX        uint64
	Since     int64 // unix seconds
}

// parseStatus reads OpenVPN's version 2 status format.
//
// The format is comma-separated with a leading tag per line. Only CLIENT_LIST
// carries per-client counters; the other tags are headers and totals. Fields are
// read by index because the format has no names, and a short line is skipped
// rather than treated as a client with zero traffic, which would look to the
// panel like an idle customer instead of a parse failure.
func parseStatus(raw string) []session {
	var out []session

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "CLIENT_LIST,") {
			continue
		}

		f := strings.Split(line, ",")
		// CLIENT_LIST,name,real,virtual,virtual6,rx,tx,since,since_unix,...
		if len(f) < 9 {
			continue
		}
		name := f[1]
		if name == "" || name == "UNDEF" {
			continue
		}

		out = append(out, session{
			Username:  name,
			RealIP:    f[2],
			VirtualIP: f[3],
			RX:        parseUint(f[5]),
			TX:        parseUint(f[6]),
			Since:     int64(parseUint(f[8])),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func parseUint(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// parseClientConfigs turns the contents of the address directory into the
// username-to-address map the diff compares against.
func parseClientConfigs(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for username, body := range files {
		if ip := addressFrom(body); ip != "" {
			out[username] = ip
		}
	}
	return out
}

// addressFrom pulls the pinned address out of a client-config-dir entry.
func addressFrom(body string) string {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "ifconfig-push" {
			return fields[1]
		}
	}
	return ""
}

// killCommand is the management command that disconnects a session.
//
// The management interface addresses sessions by common name, which is the
// username here. It is refused rather than built when the name contains a
// newline, since the protocol is line-based and a name carrying one would let a
// crafted username issue a second command.
func killCommand(username string) (string, error) {
	if username == "" {
		return "", fmt.Errorf("ovpndriver: cannot kill an empty username")
	}
	if strings.ContainsAny(username, "\r\n") {
		return "", fmt.Errorf("ovpndriver: username %q contains a line break", username)
	}
	return "kill " + username + "\n", nil
}
