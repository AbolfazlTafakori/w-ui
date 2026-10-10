package l2tpguide

import (
	"strings"
	"testing"
)

func values() Values {
	return Values{
		Tunnel: "l2tp0", Server: "vpn.example.com", Others: []string{"b.example.com"},
		ServerIP: "203.0.113.7", PSK: "SharedKey123", Username: "s9-laptop", Password: "Pa$$word1",
	}
}

// Every device that can connect is told all four things: where the server
// is, the key where it has IPsec, the username and the password.
func TestEveryDeviceGetsEverythingItNeeds(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range All() {
		if seen[g.ID] {
			t.Errorf("%s is listed twice", g.ID)
		}
		seen[g.ID] = true
		if g.Name.EN == "" || NameKey(g.ID) == "" {
			t.Errorf("%s has no name", g.ID)
		}
		if len(g.Fields) == 0 && g.Command == nil {
			if len(g.Notes) == 0 {
				t.Errorf("%s says nothing at all", g.ID)
			}
			continue // a device that cannot connect, and says what to do instead
		}
		has := map[Source]bool{}
		for _, f := range g.Fields {
			has[f.Source] = true
			if f.Label == "" || (f.Source == Fixed && f.Value == "") {
				t.Errorf("%s: a field with nothing in it: %+v", g.ID, f)
			}
		}
		if !has[Server] && !has[ServerIP] {
			t.Errorf("%s is not told the server", g.ID)
		}
		if !has[Username] || !has[Password] {
			t.Errorf("%s is not told the sign-in", g.ID)
		}
		if g.IPsec && !has[PSK] {
			t.Errorf("%s has IPsec and is not told the key", g.ID)
		}
		if !g.IPsec && has[PSK] {
			t.Errorf("%s has no IPsec and is told the key as one", g.ID)
		}
		if g.Path == "" {
			t.Errorf("%s is not told where its settings are", g.ID)
		}
	}
	for _, id := range []string{"windows", "ios", "macos", "android", "android12", "linux", "mikrotik", "keenetic",
		"tfi60", "huawei", "tplink-wan", "dlink", "tenda", "xiaomi", "asus", "openwrt"} {
		if !seen[id] {
			t.Errorf("%s is missing", id)
		}
	}
}

// The routers and modems without IPsec are listed only on a tunnel that
// accepts them; the ADSL modems, which have no client at all, always, so a
// customer with one learns what to do.
func TestDevicesWithoutIPsecOnlyWhereTheyCanConnect(t *testing.T) {
	ids := func(gs []Guide) string {
		var out []string
		for _, g := range gs {
			out = append(out, g.ID)
		}
		return "," + strings.Join(out, ",") + ","
	}
	strict, open := ids(Visible(false)), ids(Visible(true))
	for _, id := range []string{"tfi60", "huawei", "asus", "openwrt", "mikrotik-plain"} {
		if strings.Contains(strict, ","+id+",") {
			t.Errorf("%s is offered a tunnel that refuses it", id)
		}
		if !strings.Contains(open, ","+id+",") {
			t.Errorf("%s is missing where it can connect", id)
		}
	}
	for _, id := range []string{"windows", "mikrotik", adslID} {
		if !strings.Contains(strict, ","+id+",") {
			t.Errorf("%s is missing", id)
		}
	}
}

func TestRowsAreTheCustomersValues(t *testing.T) {
	v := values()
	g, _ := ByID("windows")
	got := map[string]Row{}
	for _, r := range g.Rows(v) {
		got[r.Label] = r
	}
	if r := got["Server name or address"]; r.Value != "vpn.example.com" || !r.Copy {
		t.Errorf("server: %+v", r)
	}
	if r := got["Pre-shared key"]; r.Value != "SharedKey123" || !r.Copy {
		t.Errorf("key: %+v", r)
	}
	if r := got["VPN type"]; r.Value != "L2TP/IPsec with pre-shared key" || r.Copy {
		t.Errorf("type: %+v", r)
	}
	if r := got["Connection name"]; r.Key != keyAnything {
		t.Errorf("name: %+v", r)
	}

	// A router whose field takes no name is given the numbers.
	d, _ := ByID("dlink")
	for _, r := range d.Rows(v) {
		if r.Label == "L2TP Server IP Address" && r.Value != "203.0.113.7" {
			t.Errorf("D-Link server: %+v", r)
		}
	}
	// The tunnel password is the key, unless xl2tpd could not carry it.
	h, _ := ByID("huawei")
	for _, psk := range []string{"SharedKey123", "key!123"} {
		v.PSK = psk
		for _, r := range h.Rows(v) {
			if r.Label != "Tunnel password" {
				continue
			}
			if psk == "SharedKey123" && r.Value != psk {
				t.Errorf("tunnel password: %+v", r)
			}
			if psk == "key!123" && (r.Value == psk || r.Key != keyEmpty) {
				t.Errorf("a tunnel password the server cannot prove: %+v", r)
			}
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
	v := values()
	got := PlainText(v)
	for _, want := range []string{
		"Server:          vpn.example.com", "b.example.com (if the first does not answer)",
		"Pre-shared key:  SharedKey123", "Username:        s9-laptop",
		"== Windows 10 / 11 ==", "AssumeUDPEncapsulationContextOnSendRule",
		`connect-to="vpn.example.com" user="s9-laptop" password="Pa\$\$word1" use-ipsec=yes ipsec-secret="SharedKey123"`,
		"== ADSL / VDSL modem", "One device per address",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Huawei") || strings.Contains(got, "not encrypted") {
		t.Errorf("devices without IPsec on a tunnel that refuses them:\n%s", got)
	}
	v.AllowPlain = true
	got = PlainText(v)
	for _, want := range []string{"== Huawei 4G/5G", "== Irancell TF-i60", "not encrypted", "use-ipsec=no"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q with plain L2TP accepted:\n%s", want, got)
		}
	}
}

// Every word the page shows is there in Persian too.
func TestStringsInEveryLanguage(t *testing.T) {
	s := Strings()
	for k, en := range s["en"] {
		if s["fa"][k] == "" {
			t.Errorf("%s has no Persian", k)
		}
		if en == "" {
			t.Errorf("%s has no English", k)
		}
	}
	for _, g := range All() {
		if _, ok := s["en"][NameKey(g.ID)]; !ok {
			t.Errorf("%s has no name in the dictionary", g.ID)
		}
	}
}
