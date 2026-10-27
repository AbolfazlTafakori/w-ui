package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/endpoints"
	"github.com/abolfazl/w-ui/internal/stealth"
)

// W-UI's own profile.
//
// A stock WireGuard app reads a .conf and nothing else, and a .conf has no
// place for a stealth transport. So an app of W-UI's own asks for
// ?format=wui and gets this: every WireGuard file the customer has, as the
// .conf it would get anyway, and beside each the stealth transports its tunnel
// is also reached by, with what the app needs to use them. The .conf the
// stock apps get is not touched by any of it.

// FormatWUI is the subscription format of W-UI's own profile.
const FormatWUI = "wui"

// wuiProfileVersion is the profile's format. An app refuses a version it
// does not know.
const wuiProfileVersion = 1

// WUIProfile is W-UI's own profile of one customer.
type WUIProfile struct {
	Version int    `json:"version"`
	Title   string `json:"title,omitempty"`
	// ServerTime is the panel's clock, Unix seconds, when the profile was
	// made: an app whose clock is off by more than the stealth transports
	// take keeps the difference and adds it.
	ServerTime int64       `json:"serverTime"`
	Tunnels    []WUITunnel `json:"tunnels"`
}

// WUITunnel is one WireGuard file and the ways to its tunnel.
type WUITunnel struct {
	Name   string `json:"name"`
	Device string `json:"device"`
	// WireGuard is the file a stock app would be given.
	WireGuard  string         `json:"wireguard"`
	Transports []WUITransport `json:"transports"`
}

// WUITransport is one stealth transport in front of a tunnel.
type WUITransport struct {
	// Type is the transport's name, as package stealth gives it.
	Type string `json:"type"`
	// Endpoints are where it is reached, best first.
	Endpoints []string `json:"endpoints"`
	// Secret is the device's transport secret, base64. The tunnel's public
	// key, which every key is bound to, is the one in the file.
	Secret string `json:"secret"`
	MTU    int    `json:"mtu"`
}

// wuiBundle is a customer's W-UI profile.
func (s *Subscriptions) wuiBundle(ctx context.Context, c *model.Client, byID map[uint]model.Interface, cfg SubSettings) (*Bundle, error) {
	title := strings.TrimSpace(cfg.Title)
	p := WUIProfile{Version: wuiProfileVersion, Title: title, ServerTime: time.Now().Unix(), Tunnels: []WUITunnel{}}
	for i := range c.Accounts {
		acc := c.Accounts[i]
		iface, ok := byID[acc.InterfaceID]
		if !ok || iface.Protocol != model.ProtocolWireGuard || acc.PrivateKey == "" {
			continue
		}
		drv, err := s.renderer(acc.InterfaceID, iface.Protocol)
		if err != nil {
			s.log.Warn("no driver could render a configuration", "client", c.Name, "account", acc.ID, "error", err)
			continue
		}
		// Where the customer connects, as every other format has it: the
		// operator's hosts when there are any, the tunnel's own address
		// when not. The file is for the first; wui-udp is on all of them.
		variants := endpoints.VariantsFor(&iface, FormatWUI)
		at := endpoints.WithEndpoint(iface, variants[0])
		if title != "" {
			at.ListedAs = title + " · " + iface.Name
		}
		file, err := drv.Render(ctx, &acc, &at)
		if err != nil {
			s.log.Warn("could not render a configuration for a profile", "client", c.Name, "account", acc.ID, "error", err)
			continue
		}
		t := WUITunnel{Name: iface.Name, Device: acc.DeviceName, WireGuard: string(file.Body), Transports: []WUITransport{}}
		if port := iface.StealthUDP(); port != 0 && acc.StealthSecret != "" {
			t.Transports = append(t.Transports, WUITransport{
				Type: stealth.UDP, Endpoints: stealthEndpoints(variants, port),
				Secret: acc.StealthSecret, MTU: max(iface.MTU, 0),
			})
		}
		p.Tunnels = append(p.Tunnels, t)
	}
	if len(p.Tunnels) == 0 {
		return nil, fmt.Errorf("%w: %s has no WireGuard file", ErrNotFound, c.Name)
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("service: encode profile: %w", err)
	}
	return &Bundle{
		Body:        append(body, '\n'),
		ContentType: "application/json; charset=utf-8",
		Filename:    endpoints.SafeFilename(c.Name) + ".json",
		UserInfo:    userInfo(c),
		Title:       cfg.Title,
		UpdateHours: cfg.UpdateHours,
	}, nil
}

// stealthEndpoints is where a tunnel's wui-udp is reached: every address the
// customer connects to, on the wui-udp port. A host's own port is for
// WireGuard through a forwarder, and says nothing of this one.
func stealthEndpoints(variants []endpoints.HostVariant, port int) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range variants {
		host := strings.TrimSpace(v.Endpoint)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			continue
		}
		ep := net.JoinHostPort(host, strconv.Itoa(port))
		if !seen[ep] {
			seen[ep] = true
			out = append(out, ep)
		}
	}
	return out
}
