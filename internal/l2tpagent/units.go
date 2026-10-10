package l2tpagent

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

// The agent's systemd units.
//
// The installer and the update script write them. A server updated from the
// panel's own button ran neither -- the release that installed this one knew
// nothing of L2TP -- so this build can write them too, through the update
// helper, the root channel such a server already has. Both write the same
// text: a test holds this file to the installer's.

// UnitDir is where the units go.
var UnitDir = "/etc/systemd/system"

const (
	// ServiceUnit runs the agent.
	ServiceUnit = "wui-l2tp.service"
	// PathUnit starts it while the panel describes a tunnel.
	PathUnit = "wui-l2tp.path"
)

// unitPath is what a path in a unit may be: absolute, and nothing systemd
// would read as a separator, a specifier or the end of the line.
var unitPath = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// ErrUnitPath is a binary or data directory a unit cannot name as it is.
var ErrUnitPath = errors.New("l2tpagent: a path the units would carry is not a plain absolute path")

// Units renders the two units for a binary and the panel's data directory.
func Units(binary, dataDir string) (service, watch string, err error) {
	if !unitPath.MatchString(binary) || !unitPath.MatchString(dataDir) {
		return "", "", fmt.Errorf("%w: %q, %q", ErrUnitPath, binary, dataDir)
	}
	service = `[Unit]
Description=W-UI — PPP agent: runs the L2TP and PPTP tunnels the panel describes
After=network-online.target
Wants=network-online.target
# Started by wui-l2tp.path while the panel describes a tunnel. An agent that
# fails is tried again for as long as it fails, not given up on after five.
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=` + binary + ` l2tp-agent ` + dataDir + `
# Only the agent is stopped, never xl2tpd, pptpd and the sessions they carry: a
# restart, an update's included, must not disconnect anyone. The next run
# adopts them.
KillMode=process
Restart=on-failure
RestartSec=5
`
	watch = `[Unit]
Description=W-UI — start the PPP agent while the panel describes an L2TP or PPTP tunnel

[Path]
PathExists=` + path.Join(dataDir, "l2tp", "state.json") + `
PathExists=` + path.Join(dataDir, "pptp", "state.json") + `
Unit=wui-l2tp.service

[Install]
WantedBy=multi-user.target
`
	return service, watch, nil
}

// UnitsInPlace reports whether both units are on disk as Units renders them.
func UnitsInPlace(binary, dataDir string) bool {
	service, watch, err := Units(binary, dataDir)
	if err != nil {
		return false
	}
	for name, want := range map[string]string{ServiceUnit: service, PathUnit: watch} {
		got, err := os.ReadFile(filepath.Join(UnitDir, name))
		if err != nil || string(got) != want {
			return false
		}
	}
	return true
}
