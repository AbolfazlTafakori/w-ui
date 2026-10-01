package upgradecheck

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The set every fixture holds: one of each kind of tunnel, and customers in
// each of the states an upgrade has to carry -- limited and not, a plan that
// starts on first connection, one switched off, one on two tunnels, an
// OpenVPN login. Dates are fixed, not "thirty days from now", so they read
// the same in every fixture.
var seedInterfaces = []map[string]any{
	{"name": "fx-wg", "protocol": "wireguard", "mode": "standard", "listenPort": 51901,
		"subnet": "10.71.0.0/24", "endpointHost": "vpn.example.test", "mtu": 1420, "dns": "1.1.1.1"},
	{"name": "fx-awg", "protocol": "wireguard", "mode": "amnezia", "listenPort": 51902,
		"subnet": "10.72.0.0/24", "endpointHost": "vpn.example.test"},
	{"name": "fx-ovpn", "protocol": "openvpn", "listenPort": 11940, "transport": "udp",
		"subnet": "10.73.0.0/24", "endpointHost": "vpn.example.test"},
}

// seedClient is a customer to create, on the tunnels named.
type seedClient struct {
	on   []string
	body map[string]any
}

var (
	ends     = time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	disabled = false
)

var seedClients = []seedClient{
	{[]string{"fx-wg"}, map[string]any{"name": "wg-limited", "note": "fixture — یادداشت",
		"groups": []string{"vip"}, "quotaBytes": 10 << 30, "expiresAt": ends,
		"deviceLimit": 2, "resetCycle": "monthly"}},
	{[]string{"fx-wg"}, map[string]any{"name": "wg-unlimited"}},
	{[]string{"fx-awg"}, map[string]any{"name": "awg-first-use", "quotaBytes": 5 << 30,
		"startOnFirstUse": true, "durationDays": 30}},
	{[]string{"fx-ovpn"}, map[string]any{"name": "ovpn-user", "quotaBytes": 20 << 30, "expiresAt": ends,
		"openvpnUsername": "fxovpn", "openvpnPassword": "fixture-ovpn-pass"}},
	{[]string{"fx-wg"}, map[string]any{"name": "wg-off", "enabled": &disabled, "groups": []string{"vip", "trial"}}},
	{[]string{"fx-wg", "fx-awg"}, map[string]any{"name": "two-tunnels", "deviceLimit": 3}},
}

// SeedOptions adjusts the set to where it is seeded.
type SeedOptions struct {
	// SubPort, when not zero, puts the subscription service on a port of its
	// own, as the installer does; a release from before it had one ignores it.
	SubPort int
	// Endpoint is the address the tunnels give customers to dial, in place
	// of vpn.example.test: a real address, where a test connects to them.
	Endpoint string
}

// Seed puts the set into the panel c is signed in to, and writes down what the
// panel then says about it: the manifest, each customer's configuration as
// their link served it, and a backup archive, all into dir.
//
// Anything a release does not support -- a field it did not have yet -- is
// simply ignored by that release, which is how the same set seeds v1.0.0 and
// today alike. Anything it refuses fails the seed.
func Seed(ctx context.Context, c *Client, dir, version string, admin Admin, opts SeedOptions) (*Manifest, error) {
	m := &Manifest{Version: version, Made: time.Now().UTC(), Admin: admin, Skipped: map[string]string{}}

	sub := map[string]any{}
	if err := SetUpSubscription(ctx, c, opts.SubPort, sub); err != nil {
		return nil, err
	}

	ifaceID := map[string]uint{}
	for _, def := range seedInterfaces {
		in := map[string]any{}
		for k, v := range def {
			in[k] = v
		}
		if opts.Endpoint != "" {
			in["endpointHost"] = opts.Endpoint
		}
		var out struct {
			Interface Interface `json:"interface"`
		}
		if err := c.Do(ctx, http.MethodPost, "api/interfaces", in, &out); err != nil {
			return nil, fmt.Errorf("create interface %s: %w", in["name"], err)
		}
		ifaceID[out.Interface.Name] = out.Interface.ID
	}

	for _, sc := range seedClients {
		body := map[string]any{}
		for k, v := range sc.body {
			body[k] = v
		}
		ids := make([]uint, 0, len(sc.on))
		for _, n := range sc.on {
			ids = append(ids, ifaceID[n])
		}
		body["interfaceId"] = ids[0]
		body["interfaceIds"] = ids
		if err := c.Do(ctx, http.MethodPost, "api/clients", body, nil); err != nil {
			return nil, fmt.Errorf("create customer %s: %w", body["name"], err)
		}
	}

	if err := Describe(ctx, c, m); err != nil {
		return nil, err
	}
	// What the subscription settings ended up as, for the README.
	m.Sub = sub

	// Each customer's link, and what it serves.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		return nil, err
	}
	for i := range m.Clients {
		cu := &m.Clients[i]
		var link struct {
			Link  string `json:"link"`
			Token string `json:"token"`
		}
		if err := c.Do(ctx, http.MethodGet, fmt.Sprintf("api/clients/%d/subscription", cu.ID), nil, &link); err != nil {
			return nil, fmt.Errorf("subscription link of %s: %w", cu.Name, err)
		}
		cu.SubToken, cu.SubLink = link.Token, link.Link
		got, err := FetchSubscription(ctx, link.Link)
		if err != nil {
			return nil, fmt.Errorf("fetch the subscription of %s: %w", cu.Name, err)
		}
		cu.SubStatus = got.Status
		if got.Status == http.StatusOK {
			cu.SubFile = "sub/" + cu.Name + ".conf"
			if err := os.WriteFile(filepath.Join(dir, cu.SubFile), got.Body, 0o644); err != nil {
				return nil, err
			}
		}
	}

	// A backup, as the panel takes one.
	var arch struct {
		Name string `json:"name"`
	}
	if err := c.Do(ctx, http.MethodPost, "api/backups", nil, &arch); err != nil {
		return nil, fmt.Errorf("take a backup: %w", err)
	}
	raw, err := c.Download(ctx, "api/backups/"+arch.Name)
	if err != nil {
		return nil, fmt.Errorf("download the backup: %w", err)
	}
	m.Backup = "backup.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, m.Backup), raw, 0o644); err != nil {
		return nil, err
	}
	return m, nil
}

// SetUpSubscription turns the subscription service on, as most installs have
// it, on a port of its own when subPort is not zero; into out, when given,
// goes what the panel then says its settings are.
func SetUpSubscription(ctx context.Context, c *Client, subPort int, out map[string]any) error {
	sub := map[string]any{}
	if err := c.Do(ctx, http.MethodGet, "api/subscription", nil, &sub); err != nil {
		return err
	}
	sub["enabled"] = true
	if subPort != 0 {
		sub["port"] = subPort
	}
	if out == nil {
		out = map[string]any{}
	}
	if err := c.Do(ctx, http.MethodPut, "api/subscription", sub, &out); err != nil {
		return fmt.Errorf("turn the subscription service on: %w", err)
	}
	return nil
}

// Describe fills in what the panel says about its interfaces and customers.
func Describe(ctx context.Context, c *Client, m *Manifest) error {
	var ifaces []Interface
	if err := c.Do(ctx, http.MethodGet, "api/interfaces", nil, &ifaces); err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	m.Interfaces = nil
	for _, i := range ifaces {
		if strings.HasPrefix(i.Name, "fx-") {
			m.Interfaces = append(m.Interfaces, i)
		}
	}
	var page struct {
		Items []Customer `json:"items"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/clients?perPage=1000&pageSize=1000", nil, &page); err != nil {
		return fmt.Errorf("list customers: %w", err)
	}
	m.Clients = page.Items
	return nil
}
