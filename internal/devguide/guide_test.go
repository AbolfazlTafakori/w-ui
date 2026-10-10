package devguide

import (
	"strings"
	"testing"
)

func l2tpValues() Values {
	return Values{
		Protocol: "l2tp", Tunnel: "l2tp0", Server: "vpn.example.com", Others: []string{"b.example.com"},
		ServerIP: "203.0.113.7", PSK: "SharedKey123", Username: "s9-laptop", Password: "Pa$$word1",
	}
}

func wgValues() Values {
	return Values{
		Protocol: "wireguard", Tunnel: "wg0", Server: "vpn.example.com", ServerIP: "203.0.113.7", Port: 51820,
		File: "s9-laptop-3.conf",
		WG: WGValues{PrivateKey: "cHJpdmF0ZQ==", Address: "10.66.0.3/32", DNS: "1.1.1.1", MTU: 1420,
			PublicKey: "cHVibGlj", PresharedKey: "cHNr", AllowedIPs: "0.0.0.0/0, ::/0", Keepalive: 25},
	}
}

// Every setup tells a device everything it needs for its protocol: the
// server or the file, the key where it has IPsec, the sign-in where there
// is one, and where its settings are.
func TestEverySetupGetsEverythingItNeeds(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range All() {
		if seen[g.ID] {
			t.Errorf("%s is listed twice", g.ID)
		}
		seen[g.ID] = true
		if g.Name.EN == "" {
			t.Errorf("%s has no name", g.ID)
		}
		if len(g.Setups) == 0 && len(g.Notes) == 0 {
			t.Errorf("%s says nothing at all", g.ID)
		}
		protocols := map[Protocol]bool{}
		for _, s := range g.Setups {
			if protocols[s.Protocol] {
				t.Errorf("%s has two %s setups", g.ID, s.Protocol)
			}
			protocols[s.Protocol] = true
			has := map[Source]bool{}
			for _, f := range s.Fields {
				has[f.Source] = true
				if f.Label == "" || (f.Source == Fixed && f.Value == "") {
					t.Errorf("%s %s: a field with nothing in it: %+v", g.ID, s.Protocol, f)
				}
			}
			if s.Path == "" {
				t.Errorf("%s %s is not told where its settings are", g.ID, s.Protocol)
			}
			switch s.Protocol {
			case WireGuard:
				if !has[File] && !(has[WGPrivateKey] && has[WGPublicKey]) {
					t.Errorf("%s WireGuard is given neither the file nor the keys", g.ID)
				}
			case OpenVPN:
				if !has[File] || !has[Username] || !has[Password] {
					t.Errorf("%s OpenVPN is not given the file and the sign-in", g.ID)
				}
			default:
				if !has[Server] && !has[ServerIP] {
					t.Errorf("%s %s is not told the server", g.ID, s.Protocol)
				}
				if !has[Username] || !has[Password] {
					t.Errorf("%s %s is not told the sign-in", g.ID, s.Protocol)
				}
			}
			if s.Protocol == L2TP && !has[PSK] {
				t.Errorf("%s L2TP/IPsec is not told the key", g.ID)
			}
			if s.Protocol != L2TP && has[PSK] {
				t.Errorf("%s %s is told the IPsec key", g.ID, s.Protocol)
			}
		}
	}
	if n := len(All()); n < 70 {
		t.Errorf("%d devices", n)
	}
}

// A file is offered on the devices that dial its protocol and nothing else:
// a WireGuard file is no use on a router whose only client is L2TP.
func TestViewsAreTheFilesProtocolOnly(t *testing.T) {
	for _, v := range []Values{l2tpValues(), wgValues()} {
		served := map[Protocol]bool{}
		for _, p := range v.Served() {
			served[p] = true
		}
		for _, view := range Views(v) {
			if !served[view.Protocol] {
				t.Errorf("%s file offered %s on %s", v.Protocol, view.Protocol, view.Device)
			}
		}
	}
	// L2TP without IPsec only on a tunnel that accepts it.
	for _, view := range Views(l2tpValues()) {
		if view.Protocol == L2TPPlain {
			t.Fatalf("plain L2TP offered on a tunnel that refuses it: %s", view.Device)
		}
	}
	v := l2tpValues()
	v.AllowPlain = true
	plain := 0
	for _, view := range Views(v) {
		if view.Protocol == L2TPPlain {
			plain++
			if !view.Weak {
				t.Errorf("%s: plain L2TP not marked weak", view.Device)
			}
		}
	}
	if plain < 20 {
		t.Errorf("only %d devices without IPsec", plain)
	}
}

// AmneziaWG is something only its apps speak: no router is offered it.
func TestAmneziaIsOfferedNoRouter(t *testing.T) {
	v := wgValues()
	v.WG.Amnezia = true
	for _, view := range Views(v) {
		g, _ := ByID(view.Device)
		if g.Kind == Router {
			t.Errorf("%s offered an AmneziaWG tunnel", view.Device)
		}
	}
	routers := 0
	for _, view := range Views(wgValues()) {
		if g, _ := ByID(view.Device); g.Kind == Router {
			routers++
		}
	}
	if routers < 5 {
		t.Errorf("only %d routers dial standard WireGuard", routers)
	}
}

func TestRowsAreTheCustomersValues(t *testing.T) {
	v := l2tpValues()
	row := func(views []View, device string, p Protocol, label string) Row {
		for _, view := range views {
			if view.Device == device && view.Protocol == p {
				for _, r := range view.Rows {
					if r.Label == label {
						return r
					}
				}
			}
		}
		t.Fatalf("no %q on %s %s", label, device, p)
		return Row{}
	}
	views := Views(v)
	if r := row(views, "windows", L2TP, "Server name or address"); r.Value != "vpn.example.com" || !r.Copy {
		t.Errorf("server: %+v", r)
	}
	if r := row(views, "windows", L2TP, "Pre-shared key"); r.Value != "SharedKey123" || !r.Copy {
		t.Errorf("key: %+v", r)
	}
	if r := row(views, "windows", L2TP, "Connection name"); r.Key != keyAnything {
		t.Errorf("name: %+v", r)
	}
	if r := row(views, "omada", L2TP, "Server IP"); r.Value != "203.0.113.7" {
		t.Errorf("Omada is not given numbers: %+v", r)
	}
	wg := Views(wgValues())
	if r := row(wg, "omada", WireGuard, "WireGuard: Private Key"); r.Value != "cHJpdmF0ZQ==" || !r.Copy {
		t.Errorf("WireGuard key: %+v", r)
	}
	if r := row(wg, "tplink-ax53", WireGuard, "Import from Config File"); r.Value != "s9-laptop-3.conf" || r.Copy {
		t.Errorf("WireGuard file: %+v", r)
	}
	// The tunnel password is the key, unless xl2tpd could not carry it.
	v.AllowPlain = true
	for _, psk := range []string{"SharedKey123", "key!123"} {
		v.PSK = psk
		r := row(Views(v), "huawei", L2TPPlain, "Tunnel password")
		if psk == "SharedKey123" && r.Value != psk {
			t.Errorf("tunnel password: %+v", r)
		}
		if psk == "key!123" && (r.Value == psk || r.Key != keyEmpty) {
			t.Errorf("a tunnel password the server cannot prove: %+v", r)
		}
	}
}

func TestDetectPicksTheBrowsersDevice(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36":                   "windows",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15":    "ios",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15":           "macos",
		"Mozilla/5.0 (Linux; Android 10; SM-A505F) AppleWebKit/537.36":                   "android",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36":                    "android12",
		"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36":                    "chromeos",
		"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:126.0) Gecko/20100101 Firefox/126.0": "linux",
		"curl/8.5.0": "windows",
	} {
		if got := Detect(ua); got != want {
			t.Errorf("%q: %s, want %s", ua, got, want)
		}
	}
}

func TestPlainTextCarriesEveryDevice(t *testing.T) {
	v := l2tpValues()
	got := PlainText(v)
	for _, want := range []string{
		"Server:          vpn.example.com", "b.example.com (if the first does not answer)",
		"Pre-shared key:  SharedKey123", "Username:        s9-laptop",
		"== Windows 10 / 11 ==", "AssumeUDPEncapsulationContextOnSendRule",
		`connect-to="vpn.example.com" user="s9-laptop" password="Pa\$\$word1" use-ipsec=yes ipsec-secret="SharedKey123"`,
		"== Other ADSL / VDSL modems", "One device per address",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Huawei 4G/5G") || strings.Contains(got, "not encrypted") {
		t.Errorf("devices without IPsec on a tunnel that refuses them:\n%s", got)
	}
	v.AllowPlain = true
	got = PlainText(v)
	for _, want := range []string{"== Huawei 4G/5G", "== Irancell TF-i60", "== TP-Link TL-MR6400 v9 (4G)", "not encrypted", "use-ipsec=no"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q with plain L2TP accepted:\n%s", want, got)
		}
	}
}

// Every word the page shows is there in Persian, and a Persian sentence
// opens with a Persian word: the page sets each line's direction by its
// first letter, and one that opens with a Latin name is laid out left to
// right with its words out of order.
func TestPersianEverywhere(t *testing.T) {
	persian := func(s string) bool {
		for _, r := range s {
			switch {
			case r >= 0x0600 && r <= 0x06FF:
				return true
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
				return false
			}
		}
		return true
	}
	check := func(where string, n Text) {
		if n.FA == "" {
			t.Errorf("%s has no Persian: %q", where, n.EN)
		} else if !persian(n.FA) {
			t.Errorf("%s opens in Latin: %q", where, n.FA)
		}
	}
	for _, g := range All() {
		for _, n := range g.Notes {
			check(g.ID, n)
		}
		for _, s := range g.Setups {
			for _, n := range s.Notes {
				check(g.ID+" "+string(s.Protocol), n)
			}
		}
	}
	for k, w := range words {
		check(k, w)
	}
	s := Strings()
	for _, g := range All() {
		if _, ok := s["fa"][NameKey(g.ID)]; !ok {
			t.Errorf("%s has no name in the dictionary", g.ID)
		}
	}
}

// A PPTP page is told the server takes MS-CHAPv2 with 128-bit MPPE, by a
// field or by a note; a D-Link L2TP page is told to leave MPPE off.
func TestEncryptionIsSaid(t *testing.T) {
	for _, g := range All() {
		for _, s := range g.Setups {
			if s.Protocol != PPTP {
				continue
			}
			said := false
			for _, f := range s.Fields {
				if strings.Contains(f.Value, "MPPE") || strings.Contains(f.Value, "MS-CHAPv2") || f.Value == "Encrypted" || f.Value == "on" {
					said = true
				}
			}
			for _, n := range s.Notes {
				if strings.Contains(n.EN, "MPPE") {
					said = true
				}
			}
			if !said && g.Kind == Router {
				t.Errorf("%s PPTP is not told its encryption", g.ID)
			}
		}
	}
	g, _ := ByID("dlink-842r1")
	s, _ := g.SetupFor(L2TPPlain)
	if !strings.Contains(s.Notes[0].EN, "No encryption") {
		t.Errorf("D-Link is not told to leave MPPE off: %+v", s.Notes)
	}
}
