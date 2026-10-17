package service

import (
	"bufio"
	"fmt"
	"net/url"
	"strings"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// The subscription as a proxy app reads it: v2rayNG, v2rayN, Hiddify,
// NekoBox, Streisand and the rest take a list of links, one a line, and
// nothing else. A WireGuard file in the middle of it -- its [Interface]
// block -- is text they cannot read, and most of them give up on the whole
// list. So for them the list is links alone: a 3x-ui node's, as that panel
// makes them, and each WireGuard file as a wireguard:// link, which they all
// import. What has no link form -- OpenVPN, L2TP, PPTP, IKEv2, OpenConnect,
// AmneziaWG -- stays on the customer's page and in the other formats.

// ErrNoLinks is a proxy app asking for a customer none of whose files has a
// link form. An empty list is not an answer to give it: the app takes it as
// the subscription emptied, shows nothing and gives no reason -- and drops
// the servers it had. An error it shows as a failed update, and keeps them.
// A kind of ErrNotFound, so anything else that asks is told so.
var ErrNoLinks = fmt.Errorf("%w: nothing on this subscription has a link a proxy app imports", ErrNotFound)

// linkParts is the customer's files that have a link form, as links.
func linkParts(rendered []RenderedDevice, byID map[uint]model.Interface) []backend.ClientProfile {
	var out []backend.ClientProfile
	for _, d := range rendered {
		iface := byID[d.Account.InterfaceID]
		switch {
		case iface.Protocol == model.ProtocolXray:
			out = append(out, d.Profile)
		case iface.Protocol == model.ProtocolWireGuard && iface.Mode != model.ModeAmnezia:
			name := iface.Name
			if d.Host != nil && d.Host.Name != "" {
				name += " · " + d.Host.Name
			}
			if link, ok := WireGuardLink(string(d.Profile.Body), name); ok {
				out = append(out, backend.ClientProfile{Filename: d.Profile.Filename, MIMEType: "text/plain", Body: []byte(link)})
			}
		}
	}
	return out
}

// WireGuardLink writes a WireGuard file as the wireguard:// link proxy apps
// import: the private key, the server, and the peer's key, preshared key,
// the device's address and the MTU as parameters. False when the file is
// missing something a link needs.
func WireGuardLink(conf, name string) (string, bool) {
	vals := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(conf))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if _, seen := vals[k]; !seen {
			vals[k] = strings.TrimSpace(v)
		}
	}
	priv, pub, endpoint := vals["privatekey"], vals["publickey"], vals["endpoint"]
	if priv == "" || pub == "" || endpoint == "" {
		return "", false
	}
	q := url.Values{}
	q.Set("publickey", pub)
	if psk := vals["presharedkey"]; psk != "" {
		q.Set("presharedkey", psk)
	}
	if addr := vals["address"]; addr != "" {
		q.Set("address", strings.ReplaceAll(addr, " ", ""))
	}
	if mtu := vals["mtu"]; mtu != "" {
		q.Set("mtu", mtu)
	}
	if ka := vals["persistentkeepalive"]; ka != "" {
		q.Set("keepalive", ka)
	}
	return fmt.Sprintf("wireguard://%s@%s?%s#%s", url.QueryEscape(priv), endpoint, q.Encode(), url.PathEscape(name)), true
}
