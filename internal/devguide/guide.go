// Package devguide is where a customer types what, on every system, router
// and modem the panel has a guide for, in every protocol that device has.
//
// A router or a modem imports nothing the way a phone app does: the customer
// finds a page in its settings and copies values into fields that every maker
// names and places differently, and a device whose fields they cannot match is
// a customer who cannot connect. So each device is described once, here, in
// its own words -- for each protocol it can dial, where its settings are, what
// each field is called and what goes in it -- as its maker's emulator or
// manual shows it. A customer's page offers their device and then the
// protocols of their plan that device has, one guide at a time, with their own
// values filled in. Nothing here touches the network or the disk.
package devguide

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// Protocol is what a setup dials: one of the panel's engines, or for L2TP,
// with or without IPsec.
type Protocol string

const (
	WireGuard Protocol = "wireguard"
	OpenVPN   Protocol = "openvpn"
	// L2TP is L2TP inside IPsec, with the tunnel's pre-shared key.
	L2TP Protocol = "l2tp"
	// L2TPPlain is L2TP with no IPsec: not encrypted, served only by a
	// tunnel that says it accepts it.
	L2TPPlain Protocol = "l2tp-plain"
	PPTP      Protocol = "pptp"
)

// Protocols are every protocol, in the order a device lists them.
var Protocols = []Protocol{WireGuard, OpenVPN, L2TP, L2TPPlain, PPTP}

// Weak is whether a protocol's sessions are not encrypted, or only with a
// cipher long since broken: said wherever it is offered.
func (p Protocol) Weak() bool { return p == L2TPPlain || p == PPTP }

// Values are one customer's file: what the fields are filled with.
type Values struct {
	// Protocol is the tunnel's: wireguard, openvpn, l2tp or pptp.
	Protocol string
	// Tunnel is the interface's name, for a heading.
	Tunnel string
	// Server is the address to dial; Others the ones to try if it does not
	// answer. ServerIP is it as numbers, for fields that take no name; empty
	// when Server already is numbers, or is unknown. Port is the tunnel's,
	// for WireGuard and OpenVPN.
	Server   string
	Others   []string
	ServerIP string
	Port     int
	// PSK is the L2TP tunnel's pre-shared key; AllowPlain whether the tunnel
	// also accepts L2TP without IPsec.
	PSK        string
	AllowPlain bool
	// Username and Password are the sign-in, for OpenVPN, L2TP and PPTP.
	Username string
	Password string
	// File is the name of the file this page hands out, for the devices that
	// import one: a WireGuard .conf, an OpenVPN .ovpn.
	File string
	// WG is a WireGuard file's own values, for the devices whose form has a
	// field for each.
	WG WGValues
}

// WGValues are what a WireGuard file holds.
type WGValues struct {
	PrivateKey   string
	Address      string
	DNS          string
	MTU          int
	PublicKey    string
	PresharedKey string
	AllowedIPs   string
	Keepalive    int
	// Amnezia is an AmneziaWG tunnel: its disguise is something only the
	// AmneziaWG apps speak, so no router can dial it.
	Amnezia bool
}

// Served is the setups a customer's file can be used with.
func (v Values) Served() []Protocol {
	switch v.Protocol {
	case "wireguard":
		return []Protocol{WireGuard}
	case "openvpn":
		return []Protocol{OpenVPN}
	case "l2tp":
		if v.AllowPlain {
			return []Protocol{L2TP, L2TPPlain}
		}
		return []Protocol{L2TP}
	case "pptp":
		return []Protocol{PPTP}
	}
	return nil
}

// Text is a sentence in each language the pages speak.
type Text struct {
	EN string `json:"en"`
	FA string `json:"fa,omitempty"`
}

// Kind is which list a device is in.
type Kind string

const (
	// Device is a phone, tablet or computer.
	Device Kind = "device"
	// Router is a router or a modem.
	Router Kind = "router"
)

// Source is what a field is filled with.
type Source int

const (
	// Fixed is a choice the device offers, written as it shows it.
	Fixed Source = iota
	// Server is the server's address.
	Server
	// ServerIP is the address as numbers where known, the name where not.
	ServerIP
	// Port is the tunnel's port.
	Port
	Username
	Password
	PSK
	// TunnelPassword is the pre-shared key where the server can prove it,
	// and empty where it cannot.
	TunnelPassword
	// Anything is a name the customer chooses.
	Anything
	// Empty is a field left as it is.
	Empty
	// File is the file this page hands out, to be chosen in the device's
	// import dialog.
	File
	WGPrivateKey
	WGAddress
	WGDNS
	WGMTU
	WGPublicKey
	WGPresharedKey
	WGAllowedIPs
	WGKeepalive
)

// Field is one line of a device's form.
type Field struct {
	// Label is the field as the device names it.
	Label  string
	Source Source
	// Value is the choice, for Fixed.
	Value string
}

// Setup is one protocol on one device: where its settings are and what goes
// in each field.
type Setup struct {
	Protocol Protocol
	// Path is where the settings are, in the device's own menu words.
	Path    string
	Fields  []Field
	Notes   []Text
	Command func(v Values) string
	// NoAmnezia is a WireGuard setup on a device that cannot speak
	// AmneziaWG's disguise: offered only for a standard WireGuard tunnel.
	NoAmnezia bool
}

// Guide is one device and the protocols it can dial.
type Guide struct {
	// ID is stable: it is what a page remembers a customer chose.
	ID   string
	Name Text
	Kind Kind
	// Notes are said whatever the protocol: on a device with no setups at
	// all, what to do instead.
	Notes  []Text
	Setups []Setup
}

// All is every device, in the order the pages list them.
func All() []Guide { return catalogue }

// ByID finds a device.
func ByID(id string) (Guide, bool) {
	for _, g := range catalogue {
		if g.ID == id {
			return g, true
		}
	}
	return Guide{}, false
}

// Supports is the protocols a device can dial.
func (g Guide) Supports() []Protocol {
	var out []Protocol
	for _, p := range Protocols {
		for _, s := range g.Setups {
			if s.Protocol == p {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// SetupFor is a device's setup for a protocol.
func (g Guide) SetupFor(p Protocol) (Setup, bool) {
	for _, s := range g.Setups {
		if s.Protocol == p {
			return s, true
		}
	}
	return Setup{}, false
}

// Row is one filled-in field.
type Row struct {
	Label string `json:"label"`
	// Value is what goes in the field; for words rather than a value, the
	// English, with Key naming them for the page's other languages and
	// Words carrying them for the panel, which has no such dictionary.
	Value string `json:"value"`
	Key   string `json:"key,omitempty"`
	Words *Text  `json:"words,omitempty"`
	// Copy is whether the value is the customer's own, worth a copy button.
	Copy bool `json:"copy,omitempty"`
	// Secret names a sign-in value of a PPP connection -- SecretUsername,
	// SecretPassword or SecretPSK -- which the customer's page never puts in
	// the page itself: it shows a mask, and copies the value on a click.
	Secret string `json:"secret,omitempty"`
}

// The sign-in values a customer's page withholds, by what they are.
const (
	SecretUsername = "username"
	SecretPassword = "password"
	SecretPSK      = "psk"
)

// Mask is what a page shows in a withheld value's place.
const Mask = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"

// IsPPP reports whether a protocol signs in with a username and password
// typed into the device: L2TP, with IPsec or without, and PPTP.
func (p Protocol) IsPPP() bool { return p == L2TP || p == L2TPPlain || p == PPTP }

// Masked is v with its sign-in replaced by Mask, for a command shown on a
// page that does not show the sign-in itself.
func (v Values) Masked() Values {
	v.Username, v.Password = Mask, Mask
	if v.PSK != "" {
		v.PSK = Mask
	}
	return v
}

// SecretOf is the value of a withheld field.
func (v Values) SecretOf(kind string) (string, bool) {
	switch kind {
	case SecretUsername:
		return v.Username, v.Username != ""
	case SecretPassword:
		return v.Password, v.Password != ""
	case SecretPSK:
		return v.PSK, v.PSK != ""
	}
	return "", false
}

// Rows fills a setup's fields with a customer's values.
func (s Setup) Rows(v Values) []Row {
	out := make([]Row, 0, len(s.Fields))
	ppp := s.Protocol.IsPPP()
	for _, f := range s.Fields {
		r := Row{Label: f.Label}
		own := func(val string) { r.Value, r.Copy = val, true }
		secret := func(kind string) {
			if ppp {
				r.Secret = kind
			}
		}
		switch f.Source {
		case Fixed:
			r.Value = f.Value
		case Server:
			own(v.Server)
		case ServerIP:
			own(v.Server)
			if v.ServerIP != "" {
				r.Value = v.ServerIP
			}
		case Port:
			own(strconv.Itoa(v.Port))
		case Username:
			own(v.Username)
			secret(SecretUsername)
		case Password:
			own(v.Password)
			secret(SecretPassword)
		case PSK:
			own(v.PSK)
			secret(SecretPSK)
		case TunnelPassword:
			// A masked key stands for one that is usable: it is masked only
			// where the real one would have been shown.
			if v.PSK == Mask || l2tpconf.TunnelSecretUsable(v.PSK) {
				own(v.PSK)
				secret(SecretPSK)
			} else {
				r.Key = keyEmpty
			}
		case Anything:
			r.Key = keyAnything
		case Empty:
			r.Key = keyEmpty
		case File:
			own(v.File)
			r.Copy = false
		case WGPrivateKey:
			own(v.WG.PrivateKey)
		case WGAddress:
			own(v.WG.Address)
		case WGDNS:
			own(v.WG.DNS)
		case WGMTU:
			own(strconv.Itoa(v.WG.MTU))
		case WGPublicKey:
			own(v.WG.PublicKey)
		case WGPresharedKey:
			own(v.WG.PresharedKey)
		case WGAllowedIPs:
			own(v.WG.AllowedIPs)
		case WGKeepalive:
			own(strconv.Itoa(v.WG.Keepalive))
		}
		if r.Key != "" {
			w := words[r.Key]
			r.Value, r.Words = w.EN, &w
		}
		out = append(out, r)
	}
	return out
}

// Group is which list a device is shown under.
type Group string

const (
	// GroupDevice is phones and computers.
	GroupDevice Group = "device"
	// GroupRouter is routers and modems.
	GroupRouter Group = "router"
	// GroupNone is the models with no VPN client at all, listed so their
	// owners learn what to do instead.
	GroupNone Group = "none"
)

// GroupOf is the list a device is shown under.
func GroupOf(g Guide) Group {
	switch {
	case g.Kind == Device:
		return GroupDevice
	case len(g.Setups) == 0:
		return GroupNone
	}
	return GroupRouter
}

// Entry is one device of the list, with no customer in it: the device picker
// the pages open with.
type Entry struct {
	ID       string     `json:"id"`
	Name     Text       `json:"name"`
	Group    Group      `json:"group"`
	Supports []Protocol `json:"supports"`
	Notes    []Text     `json:"notes,omitempty"`
}

// Devices is every device there is a guide for.
func Devices() []Entry {
	out := make([]Entry, 0, len(catalogue))
	for _, g := range catalogue {
		sup := g.Supports()
		if sup == nil {
			sup = []Protocol{}
		}
		out = append(out, Entry{ID: g.ID, Name: g.Name, Group: GroupOf(g), Supports: sup, Notes: g.Notes})
	}
	return out
}

// View is one device's setup for one protocol, filled in with a customer's
// values: what a page shows when that device and that file are chosen.
type View struct {
	Device   string   `json:"device"`
	Protocol Protocol `json:"protocol"`
	Path     string   `json:"path,omitempty"`
	Rows     []Row    `json:"rows,omitempty"`
	Notes    []Text   `json:"notes,omitempty"`
	Command  string   `json:"command,omitempty"`
	// Weak is a protocol that is not encrypted, or barely: said above it.
	Weak bool `json:"weak,omitempty"`
}

// Views is every setup a customer's file can be used with, filled in: for
// each device, each protocol of the file it has.
func Views(v Values) []View {
	var out []View
	for _, g := range catalogue {
		for _, p := range v.Served() {
			s, ok := g.SetupFor(p)
			if !ok || (s.NoAmnezia && v.WG.Amnezia) {
				continue
			}
			view := View{Device: g.ID, Protocol: p, Path: s.Path, Rows: s.Rows(v), Notes: s.Notes, Weak: p.Weak()}
			if s.Command != nil {
				view.Command = s.Command(v)
			}
			out = append(out, view)
		}
	}
	return out
}

// Detect picks the device a browser most likely is, for the page to open on.
// Android 12 and newer have no L2TP or PPTP, and are a device of their own.
func Detect(userAgent string) string {
	ua := userAgent
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		return "ios"
	case strings.Contains(ua, "Android"):
		if androidMajor(ua) >= 12 {
			return "android12"
		}
		return "android"
	case strings.Contains(ua, "CrOS"):
		return "chromeos"
	case strings.Contains(ua, "Macintosh"):
		return "macos"
	case strings.Contains(ua, "Windows"):
		return "windows"
	case strings.Contains(ua, "Linux"):
		return "linux"
	}
	return "windows"
}

// androidMajor is the version in "Android 13; ...", or 0.
func androidMajor(ua string) int {
	i := strings.Index(ua, "Android ")
	if i < 0 {
		return 0
	}
	rest := ua[i+len("Android "):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// ProtocolNames are how the pages name each protocol.
var ProtocolNames = map[Protocol]Text{
	WireGuard: {EN: "WireGuard", FA: "وایرگارد"},
	OpenVPN:   {EN: "OpenVPN", FA: "OpenVPN"},
	L2TP:      {EN: "L2TP/IPsec", FA: "L2TP/IPsec"},
	L2TPPlain: {EN: "L2TP without IPsec (not encrypted)", FA: "L2TP بدون IPsec (بدون رمزگذاری)"},
	PPTP:      {EN: "PPTP (weak encryption)", FA: "PPTP (رمزگذاری ضعیف)"},
}

// PlainText is an L2TP or PPTP sign-in as a file, for the download and the
// Telegram bot: the sign-in, then every device that dials it, filled in, in
// English.
// WithheldText is PlainText without the sign-in, for where anyone holding
// the link -- an app, a script, a download -- reads it rather than the
// customer: the username, password and key are on the customer's page, which
// shows them only on a click.
func WithheldText(v Values) string {
	return "-- The username, password and key are not in this file. Open this link in a browser\n" +
		"-- and copy them from your page there.\n\n" + PlainText(v.Masked())
}

func PlainText(v Values) string {
	var b strings.Builder
	pptp := v.Protocol == "pptp"
	if pptp {
		fmt.Fprintf(&b, "PPTP — %s\n\n", v.Tunnel)
		b.WriteString("-- PPTP's encryption has been broken for years: use it only where nothing else will do.\n\n")
	} else {
		fmt.Fprintf(&b, "L2TP/IPsec — %s\n\n", v.Tunnel)
	}
	fmt.Fprintf(&b, "Server:          %s\n", v.Server)
	for _, s := range v.Others {
		fmt.Fprintf(&b, "                 %s (if the first does not answer)\n", s)
	}
	if v.ServerIP != "" {
		fmt.Fprintf(&b, "Server IP:       %s (for a router that takes no name)\n", v.ServerIP)
	}
	if !pptp {
		fmt.Fprintf(&b, "Pre-shared key:  %s\n", v.PSK)
	}
	fmt.Fprintf(&b, "Username:        %s\n", v.Username)
	fmt.Fprintf(&b, "Password:        %s\n", v.Password)

	names := map[string]string{}
	for _, g := range catalogue {
		names[g.ID] = g.Name.EN
	}
	for _, p := range v.Served() {
		if p == L2TPPlain {
			b.WriteString("\n-- Without IPsec, which this server accepts too: the connection is not encrypted.\n")
			b.WriteString("-- Use it only where nothing else will do.\n")
		}
		for _, view := range Views(v) {
			if view.Protocol != p {
				continue
			}
			fmt.Fprintf(&b, "\n== %s ==\n", names[view.Device])
			if view.Path != "" {
				fmt.Fprintf(&b, "%s\n", view.Path)
			}
			for _, r := range view.Rows {
				fmt.Fprintf(&b, "  %-26s %s\n", r.Label+":", r.Value)
			}
			for _, n := range view.Notes {
				fmt.Fprintf(&b, "  %s\n", n.EN)
			}
			if view.Command != "" {
				fmt.Fprintf(&b, "  %s\n", view.Command)
			}
		}
	}
	none := false
	for _, g := range catalogue {
		if GroupOf(g) != GroupNone {
			continue
		}
		if !none {
			none = true
			b.WriteString("\n-- These have no VPN client of their own:\n")
		}
		fmt.Fprintf(&b, "\n== %s ==\n", g.Name.EN)
		for _, n := range g.Notes {
			fmt.Fprintf(&b, "  %s\n", n.EN)
		}
	}
	if !pptp {
		b.WriteString("\n" + words[keyOneDevice].EN + "\n")
	}
	return b.String()
}

// Strings is every word the customer's page shows for these devices, keyed
// for its language switch.
func Strings() map[string]map[string]string {
	out := map[string]map[string]string{"en": {}, "fa": {}}
	put := func(key string, t Text) {
		out["en"][key] = t.EN
		fa := t.FA
		if fa == "" {
			fa = t.EN
		}
		out["fa"][key] = fa
	}
	for k, t := range words {
		put(k, t)
	}
	for p, t := range ProtocolNames {
		put(ProtocolKey(p), t)
	}
	for _, g := range catalogue {
		put(NameKey(g.ID), g.Name)
		for i, n := range g.Notes {
			put(NoteKey(g.ID, "", i), n)
		}
		for _, s := range g.Setups {
			for i, n := range s.Notes {
				put(NoteKey(g.ID, s.Protocol, i), n)
			}
		}
	}
	return out
}

// NameKey, NoteKey and ProtocolKey are words in the page's dictionary.
func NameKey(id string) string { return "dg." + id }
func NoteKey(id string, p Protocol, i int) string {
	if p == "" {
		return fmt.Sprintf("dg.%s.%d", id, i)
	}
	return fmt.Sprintf("dg.%s.%s.%d", id, p, i)
}
func ProtocolKey(p Protocol) string { return "dgp." + string(p) }
