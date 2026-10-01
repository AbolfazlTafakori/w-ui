package upgradecheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Manifest is what a panel said about the set Seed put into it, written down
// when the fixture was made. A later panel is checked against it.
//
// Keys, tokens and dates are whatever that panel generated: they are not
// made deterministic, they are recorded, and the later panel must give back
// the same ones -- a customer's keys and links changing under them is exactly
// the breakage these tests are for.
type Manifest struct {
	// Version is the release that made the fixture.
	Version string `json:"version"`
	// Made is when, for the README; nothing is compared against it.
	Made time.Time `json:"made"`

	Admin      Admin             `json:"admin"`
	Interfaces []Interface       `json:"interfaces"`
	Clients    []Customer        `json:"clients"`
	Settings   map[string]string `json:"settings"`
	Sub        map[string]any    `json:"subscriptionSettings"`
	// Pages is what each settings page said it held once every setting on
	// it was saved with a value that is not its default (SeedSettings).
	Pages   map[string]any    `json:"pages,omitempty"`
	Backup  string            `json:"backup,omitempty"`
	Skipped map[string]string `json:"skipped,omitempty"`
	Extra   map[string]any    `json:"extra,omitempty"`
}

// Admin is the fixture's administrator. A test credential, written into the
// fixture on purpose.
type Admin struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Interface is one tunnel, as the panel described it.
type Interface struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Mode       string `json:"mode"`
	ListenPort int    `json:"listenPort"`
	Subnet     string `json:"subnet"`
	PublicKey  string `json:"publicKey"`
}

// Customer is one customer, as the panel described them, with the
// subscription link it handed out and the configuration that link served.
type Customer struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Note            string     `json:"note"`
	Protocol        string     `json:"protocol"`
	Status          string     `json:"status"`
	Groups          []string   `json:"groups"`
	QuotaBytes      uint64     `json:"quotaBytes"`
	UsedBytes       uint64     `json:"usedBytes"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	StartOnFirstUse bool       `json:"startOnFirstUse"`
	DurationDays    int        `json:"durationDays"`
	DeviceLimit     int        `json:"deviceLimit"`
	ResetCycle      string     `json:"resetCycle"`
	SubID           string     `json:"subId"`
	Accounts        []Account  `json:"accounts"`
	// OriginID is, on a node, the id the managing panel knows the customer
	// by; a node names them after it.
	OriginID uint `json:"originId,omitempty"`

	// SubToken and SubLink are what the panel gave out for this customer.
	SubToken string `json:"subToken"`
	SubLink  string `json:"subLink"`
	// SubFile is the configuration the link served, saved beside the
	// manifest; SubStatus is the answer's status, for one with nothing to
	// serve yet.
	SubFile   string `json:"subFile,omitempty"`
	SubStatus int    `json:"subStatus"`
}

// Account is one device of a customer.
type Account struct {
	ID          uint   `json:"id"`
	InterfaceID uint   `json:"interfaceId"`
	DeviceName  string `json:"deviceName"`
	IP          string `json:"ip"`
	PublicKey   string `json:"publicKey"`
	Username    string `json:"username"`
}

// Load reads dir/manifest.json.
func Load(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return &m, nil
}

// Save writes dir/manifest.json, indented and in a stable order, so a
// regenerated fixture shows as a readable diff.
func (m *Manifest) Save(dir string) error {
	sort.Slice(m.Clients, func(i, j int) bool { return m.Clients[i].ID < m.Clients[j].ID })
	sort.Slice(m.Interfaces, func(i, j int) bool { return m.Interfaces[i].ID < m.Interfaces[j].ID })
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(raw, '\n'), 0o644)
}

// StoredSettings is the settings table as it is: every key and its value,
// under the names the panel stores them by.
func StoredSettings(db *gorm.DB) (map[string]string, error) {
	var rows []struct {
		Key   string
		Value string
	}
	if err := db.Table("settings").Select("key, value").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}
