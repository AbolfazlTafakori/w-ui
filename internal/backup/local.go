package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Keeping the addresses that belong to this machine.
//
// A backup is a copy of a whole panel, and most of it is the same wherever it
// is put back: the customers, their allowances, their keys. A few things are
// not. The address a customer dials, the spare addresses kept for when the
// first is blocked, the subscription host — those describe the server the
// backup was taken on, and restoring them onto a different server hands every
// customer a configuration pointing at the old one.
//
// That is the ordinary reason to restore onto another machine in the first
// place: the old server was blocked or lost. Faithfully restoring its address
// is restoring the problem.
//
// So the addresses are read before the restore is staged, carried through the
// restart beside it, and put back afterwards. Off is available and is the right
// answer when the archive is being restored onto the same machine it came from,
// or when an operator is deliberately cloning one server onto another.

// localAddressesFile sits in the staging directory next to the marker.
const localAddressesFile = ".local-addresses.json"

// LocalAddresses is what this machine says about where to reach it.
type LocalAddresses struct {
	// Interfaces maps a tunnel's name to the endpoint customers dial. Keyed by
	// name rather than id: the restored database has its own ids, and the name
	// is what an operator recognises on both sides.
	Interfaces map[string]string `json:"interfaces"`

	// Hosts are the spare addresses, keyed by the tunnel's name.
	Hosts map[string][]LocalHost `json:"hosts"`

	// Settings are the panel-level values that name this server.
	Settings map[string]string `json:"settings"`

	// LocalNodeAddress is this panel's own entry in the node list.
	LocalNodeAddress string `json:"localNodeAddress"`
}

// LocalHost is one spare address, without the ids that will not survive.
type LocalHost struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// hostBoundSettings are the settings that describe this server rather than this
// deployment.
//
// The panel's own listen address, base path and certificate are not here: they
// come from the environment on every start, so a restore cannot change them.
// These are the ones kept in the database.
var hostBoundSettings = []string{
	"sub.host",
	"sub.reverseProxyUri",
}

// ReadLocalAddresses takes a note of where this machine says it can be reached.
func ReadLocalAddresses(db *gorm.DB) (*LocalAddresses, error) {
	out := &LocalAddresses{
		Interfaces: map[string]string{},
		Hosts:      map[string][]LocalHost{},
		Settings:   map[string]string{},
	}

	var interfaces []model.Interface
	if err := db.Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("backup: read interfaces: %w", err)
	}
	byID := make(map[uint]string, len(interfaces))
	for _, iface := range interfaces {
		byID[iface.ID] = iface.Name
		out.Interfaces[iface.Name] = iface.EndpointHost
	}

	var hosts []model.Host
	if err := db.Find(&hosts).Error; err != nil {
		return nil, fmt.Errorf("backup: read hosts: %w", err)
	}
	for _, h := range hosts {
		name, ok := byID[h.InterfaceID]
		if !ok {
			continue
		}
		out.Hosts[name] = append(out.Hosts[name], LocalHost{
			Name: h.Name, Address: h.Address, Port: h.Port,
			Priority: h.Priority, Enabled: h.Enabled,
		})
	}

	var settings []model.Setting
	if err := db.Where("key IN ?", hostBoundSettings).Find(&settings).Error; err != nil {
		return nil, fmt.Errorf("backup: read settings: %w", err)
	}
	for _, s := range settings {
		out.Settings[s.Key] = s.Value
	}

	var local model.Node
	if err := db.Where("kind = ?", model.KindLocal).Order("id").First(&local).Error; err == nil {
		out.LocalNodeAddress = local.Address
	}

	return out, nil
}

// stashLocalAddresses writes them beside the staged restore.
func stashLocalAddresses(staging string, a *LocalAddresses) error {
	if a == nil {
		return nil
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("backup: record this server's addresses: %w", err)
	}
	return os.WriteFile(filepath.Join(staging, localAddressesFile), raw, 0o600)
}

// readStashedAddresses reads them back after the restart, or nil when the
// restore was asked to take the archive's addresses as they are.
func readStashedAddresses(staging string) *LocalAddresses {
	raw, err := os.ReadFile(filepath.Join(staging, localAddressesFile))
	if err != nil {
		return nil
	}
	var a LocalAddresses
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil
	}
	return &a
}

// ApplyLocalAddresses puts this machine's addresses back over the restored
// ones, matching tunnels by name.
//
// A tunnel in the archive that this machine never had keeps the archive's
// address: there is nothing local to put back, and blanking it would leave a
// customer with a configuration naming nowhere at all. That is the case an
// operator has to finish by hand, and the count is returned so it can be said.
func ApplyLocalAddresses(db *gorm.DB, a *LocalAddresses) (kept, unknown int, err error) {
	if a == nil {
		return 0, 0, nil
	}

	var interfaces []model.Interface
	if err := db.Find(&interfaces).Error; err != nil {
		return 0, 0, fmt.Errorf("backup: read restored interfaces: %w", err)
	}

	for _, iface := range interfaces {
		endpoint, ok := a.Interfaces[iface.Name]
		if !ok {
			unknown++
			continue
		}
		if endpoint != "" && endpoint != iface.EndpointHost {
			if err := db.Model(&model.Interface{}).Where("id = ?", iface.ID).
				Update("endpoint_host", endpoint).Error; err != nil {
				return kept, unknown, fmt.Errorf("backup: restore the address of %s: %w", iface.Name, err)
			}
		}
		kept++

		// The spares are replaced wholesale rather than merged: they are a list
		// of ways to reach one machine, and half of this server's with half of
		// the other's is a list that reaches neither reliably.
		spares, has := a.Hosts[iface.Name]
		if !has {
			continue
		}
		if err := db.Where("interface_id = ?", iface.ID).Delete(&model.Host{}).Error; err != nil {
			return kept, unknown, fmt.Errorf("backup: clear the spare addresses of %s: %w", iface.Name, err)
		}
		for _, h := range spares {
			row := model.Host{
				InterfaceID: iface.ID, Name: h.Name, Address: h.Address,
				Port: h.Port, Priority: h.Priority, Enabled: h.Enabled,
			}
			if err := db.Create(&row).Error; err != nil {
				return kept, unknown, fmt.Errorf("backup: restore a spare address of %s: %w", iface.Name, err)
			}
		}
	}

	for key, value := range a.Settings {
		if err := db.Save(&model.Setting{Key: key, Value: value}).Error; err != nil {
			return kept, unknown, fmt.Errorf("backup: restore setting %s: %w", key, err)
		}
	}

	if a.LocalNodeAddress != "" {
		if err := db.Model(&model.Node{}).Where("kind = ?", model.KindLocal).
			Update("address", a.LocalNodeAddress).Error; err != nil {
			return kept, unknown, fmt.Errorf("backup: restore this server's own entry: %w", err)
		}
	}

	return kept, unknown, nil
}
