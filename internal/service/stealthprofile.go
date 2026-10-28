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
	// URL is a stream transport's address, wss:// with its path, or ws://
	// for wui-plain: the name TLS and the request give, which may differ
	// from the endpoint connected to. An address in it is sent as no name.
	URL string `json:"url,omitempty"`
	// Pins are wui-tls's: the SHA-256 of the server certificate's
	// SubjectPublicKeyInfo, standard base64. With SelfSigned the server is
	// trusted for its pin alone, over TLS 1.3, and its chain, name and
	// dates are not looked at; without, its certificate is checked as a
	// browser checks it, and a pin, when there is one, is a second
	// condition, never a way around the first.
	Pins       []string `json:"pins,omitempty"`
	SelfSigned bool     `json:"selfSigned,omitempty"`
	// Key is a stream transport's public key, base64, which its proof is
	// sealed to.
	Key string `json:"key,omitempty"`
	// Cookie is the name of the cookie wui-plain's token travels in.
	Cookie string `json:"cookie,omitempty"`
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
		if (iface.StealthTLS() != 0 || iface.StealthWS() != 0 || iface.StealthPlain() != 0) && acc.StealthSecret != "" {
			if wt, ok := wsTransport(iface.Stealth.V); ok {
				wt.Secret, wt.MTU = acc.StealthSecret, max(iface.MTU, 0)
				t.Transports = append(t.Transports, wt)
			} else {
				s.log.Warn("a stealth transport has no usable key yet; profiles leave it out until it has", "interface", iface.Name, "transport", iface.Stealth.V.Transport)
			}
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

// wsTransport is a tunnel's wui-tls, wui-ws or wui-plain as a profile gives
// it, but the device's own secret: where devices reach it and the name TLS
// gives, the transport's current public key, wui-plain's cookie, and
// wui-tls's pin. False when it has no key or certificate yet.
func wsTransport(p model.StealthParams) (WUITransport, bool) {
	key, err := stealth.ParseTransportKey(p.Key)
	if err != nil {
		return WUITransport{}, false
	}
	scheme, name, usual := "wss://", stealth.WS, 443
	port := p.FrontPortOr()
	var pins []string
	selfSigned := false
	switch p.Transport {
	case model.TransportWuiPlain:
		if p.Cookie == "" {
			return WUITransport{}, false
		}
		scheme, name, usual = "ws://", stealth.Plain, 80
	case model.TransportWuiTLS:
		// The gateway is the server, on its own port.
		name, port = stealth.TLS, p.Port
		cert, _ := p.ServedCert()
		_, info, err := stealth.ReadCertificate(cert)
		if err != nil {
			return WUITransport{}, false
		}
		// The tunnel's own certificate is trusted for its pin alone; the
		// panel's is checked as a browser checks it, and is given no pin,
		// since each of its renewals brings a new key.
		if p.CertSource != model.CertSourcePanel {
			pins, selfSigned = []string{info.Pin.String()}, true
		}
	}
	host := p.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // an IPv6 address
	}
	u := scheme + host
	if port != usual {
		u += ":" + strconv.Itoa(port)
	}
	t := WUITransport{
		Type:       name,
		Endpoints:  []string{net.JoinHostPort(p.Host, strconv.Itoa(port))},
		URL:        u + p.Path,
		Key:        key.Public(),
		Pins:       pins,
		SelfSigned: selfSigned,
	}
	if name == stealth.Plain {
		t.Cookie = p.Cookie
	}
	return t, true
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
