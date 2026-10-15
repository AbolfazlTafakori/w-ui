package fwsync

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

// The helper's systemd units. The installer writes the same text; a test
// holds the two together.
const (
	// ServiceUnit opens the ports, once, and is done.
	ServiceUnit = "wui-firewall.service"
	// PathUnit starts it whenever the panel writes the ports.
	PathUnit = "wui-firewall.path"
)

var unitPath = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// ErrUnitPath is a binary or data directory a unit cannot name as it is.
var ErrUnitPath = errors.New("fwsync: a path the units would carry is not a plain absolute path")

// Units renders the two units for a binary and the panel's data directory.
func Units(binary, dataDir string) (service, watch string, err error) {
	if !unitPath.MatchString(binary) || !unitPath.MatchString(dataDir) {
		return "", "", fmt.Errorf("%w: %q, %q", ErrUnitPath, binary, dataDir)
	}
	service = `[Unit]
Description=W-UI — let the tunnels' ports through ufw or firewalld
After=network-online.target ufw.service firewalld.service

[Service]
Type=oneshot
ExecStart=` + binary + ` firewall-sync ` + dataDir + `

[Install]
WantedBy=multi-user.target
`
	watch = `[Unit]
Description=W-UI — let the tunnels' ports through the firewall whenever they change

[Path]
PathChanged=` + path.Join(dataDir, DesiredFile) + `
Unit=wui-firewall.service

[Install]
WantedBy=multi-user.target
`
	return service, watch, nil
}

// UnitsInPlace reports whether both units are on disk as Units renders them.
func UnitsInPlace(unitDir, binary, dataDir string) bool {
	service, watch, err := Units(binary, dataDir)
	if err != nil {
		return false
	}
	for name, want := range map[string]string{ServiceUnit: service, PathUnit: watch} {
		got, err := os.ReadFile(filepath.Join(unitDir, name))
		if err != nil || string(got) != want {
			return false
		}
	}
	return true
}
