// Package l2tpguide is where an L2TP customer types what, on every system,
// router and modem that has an L2TP client of its own.
//
// An L2TP client imports no file: a customer copies four things -- server,
// pre-shared key, username, password -- into fields that every device names
// and places differently, and a device whose fields they cannot match is a
// customer who cannot connect. So each device is described once, here, in its
// own words: where its settings are, what each field is called, and what goes
// in it. The customer's page shows one device at a time with their own values
// filled in; the text file and the Telegram bot carry all of them. Nothing
// here touches the network or the disk.
package l2tpguide

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// Values are one customer's: what the fields are filled with.
type Values struct {
	// Tunnel is the interface's name, for the text's heading.
	Tunnel string
	// Server is the address to dial; Others the ones to try if it does not
	// answer.
	Server string
	Others []string
	// ServerIP is the server's address as numbers, for routers whose field
	// takes no name; empty when Server already is numbers, or is unknown.
	ServerIP string
	PSK      string
	Username string
	Password string
	// AllowPlain is whether the tunnel accepts L2TP without IPsec, and so
	// whether the routers and modems that have nothing else are listed.
	AllowPlain bool
}

// Text is a sentence in each language the customer's page speaks.
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
)

// Field is one line of a device's form.
type Field struct {
	// Label is the field as the device names it.
	Label  string
	Source Source
	// Value is the choice, for Fixed.
	Value string
}

// Guide is one device: where its settings are and what goes in each field.
type Guide struct {
	// ID is stable: it is what the page remembers a customer chose.
	ID   string
	Name Text
	Kind Kind
	// IPsec is false for a client that has none. Those are listed only on a
	// tunnel that accepts L2TP without IPsec.
	IPsec bool
	// Path is where the settings are, in the device's own menu words.
	Path   string
	Fields []Field
	Notes  []Text
	// Command is something to paste, where the device takes one.
	Command func(v Values) string
}

// All is every device, in the order the customer's page lists them: phones
// and computers, then routers with IPsec, then the routers and modems
// without it.
func All() []Guide { return catalogue }

// Visible is the devices a tunnel serves.
func Visible(allowPlain bool) []Guide {
	out := make([]Guide, 0, len(catalogue))
	for _, g := range catalogue {
		// A model with no client at all is listed everywhere, so a
		// customer who has one learns what to do instead.
		if g.IPsec || allowPlain || len(g.Fields) == 0 {
			out = append(out, g)
		}
	}
	return out
}

// ByID finds a device.
func ByID(id string) (Guide, bool) {
	for _, g := range catalogue {
		if g.ID == id {
			return g, true
		}
	}
	return Guide{}, false
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
}

// Rows fills a device's fields with a customer's values.
func (g Guide) Rows(v Values) []Row {
	out := make([]Row, 0, len(g.Fields))
	for _, f := range g.Fields {
		r := Row{Label: f.Label}
		switch f.Source {
		case Fixed:
			r.Value = f.Value
		case Server:
			r.Value, r.Copy = v.Server, true
		case ServerIP:
			r.Value, r.Copy = v.Server, true
			if v.ServerIP != "" {
				r.Value = v.ServerIP
			}
		case Username:
			r.Value, r.Copy = v.Username, true
		case Password:
			r.Value, r.Copy = v.Password, true
		case PSK:
			r.Value, r.Copy = v.PSK, true
		case TunnelPassword:
			if l2tpconf.TunnelSecretUsable(v.PSK) {
				r.Value, r.Copy = v.PSK, true
			} else {
				r.Key = keyEmpty
			}
		case Anything:
			r.Key = keyAnything
		case Empty:
			r.Key = keyEmpty
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
	// GroupRouter is routers with IPsec, and the modems with no client.
	GroupRouter Group = "router"
	// GroupPlain is routers and modems without IPsec: not encrypted.
	GroupPlain Group = "plain"
)

// GroupOf is the list a device is shown under.
func GroupOf(g Guide) Group {
	switch {
	case g.Kind == Device:
		return GroupDevice
	case g.IPsec || len(g.Fields) == 0:
		return GroupRouter
	}
	return GroupPlain
}

// View is one device filled in with a customer's values, ready to show:
// on the customer's page and in the panel alike.
type View struct {
	ID      string `json:"id"`
	Name    Text   `json:"name"`
	Group   Group  `json:"group"`
	Path    string `json:"path,omitempty"`
	Rows    []Row  `json:"rows,omitempty"`
	Notes   []Text `json:"notes,omitempty"`
	Command string `json:"command,omitempty"`
}

// Views is every device a tunnel serves, filled in.
func Views(v Values) []View {
	visible := Visible(v.AllowPlain)
	out := make([]View, 0, len(visible))
	for _, g := range visible {
		view := View{ID: g.ID, Name: g.Name, Group: GroupOf(g), Path: g.Path, Rows: g.Rows(v), Notes: g.Notes}
		if g.Command != nil {
			view.Command = g.Command(v)
		}
		out = append(out, view)
	}
	return out
}

// Entry is one device of the list, with no customer in it: what a tunnel's
// form shows it serves.
type Entry struct {
	ID    string `json:"id"`
	Name  Text   `json:"name"`
	Group Group  `json:"group"`
	// NoClient is a model with no L2TP client: listed so its owner learns
	// what to do instead, never as something a tunnel serves.
	NoClient bool `json:"noClient,omitempty"`
}

// Devices is every device there is a guide for.
func Devices() []Entry {
	out := make([]Entry, 0, len(catalogue))
	for _, g := range catalogue {
		out = append(out, Entry{ID: g.ID, Name: g.Name, Group: GroupOf(g), NoClient: g.Kind == Router && len(g.Fields) == 0})
	}
	return out
}

// Detect picks the device a browser most likely is, for the page to open on.
// Android 12 and newer have no L2TP, and are told so rather than shown a form
// they will not find.
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

// Plain-text rendering, for the downloaded file and the Telegram bot: every
// device the tunnel serves, filled in, in English.
func PlainText(v Values) string {
	var b strings.Builder
	fmt.Fprintf(&b, "L2TP/IPsec — %s\n\n", v.Tunnel)
	fmt.Fprintf(&b, "Server:          %s\n", v.Server)
	for _, s := range v.Others {
		fmt.Fprintf(&b, "                 %s (if the first does not answer)\n", s)
	}
	if v.ServerIP != "" {
		fmt.Fprintf(&b, "Server IP:       %s (for a router that takes no name)\n", v.ServerIP)
	}
	fmt.Fprintf(&b, "Pre-shared key:  %s\n", v.PSK)
	fmt.Fprintf(&b, "Username:        %s\n", v.Username)
	fmt.Fprintf(&b, "Password:        %s\n", v.Password)

	plainShown := false
	for _, g := range Visible(v.AllowPlain) {
		if !g.IPsec && len(g.Fields) > 0 && !plainShown {
			plainShown = true
			b.WriteString("\n-- Without IPsec, which this server accepts too: the connection is not encrypted.\n")
			b.WriteString("-- Use it only where nothing else will do.\n")
		}
		fmt.Fprintf(&b, "\n== %s ==\n", g.Name.EN)
		if g.Path != "" {
			fmt.Fprintf(&b, "%s\n", g.Path)
		}
		for _, r := range g.Rows(v) {
			fmt.Fprintf(&b, "  %-26s %s\n", r.Label+":", r.Value)
		}
		for _, n := range g.Notes {
			fmt.Fprintf(&b, "  %s\n", n.EN)
		}
		if g.Command != nil {
			fmt.Fprintf(&b, "  %s\n", g.Command(v))
		}
	}
	b.WriteString("\n" + words[keyOneDevice].EN + "\n")
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
	for _, g := range catalogue {
		put(NameKey(g.ID), g.Name)
		for i, n := range g.Notes {
			put(NoteKey(g.ID, i), n)
		}
	}
	return out
}

// NameKey and NoteKey are a device's words in the page's dictionary.
func NameKey(id string) string        { return "l2tpg." + id }
func NoteKey(id string, i int) string { return fmt.Sprintf("l2tpg.%s.%d", id, i) }
