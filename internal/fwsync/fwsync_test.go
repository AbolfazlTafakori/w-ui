package fwsync

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func iface(p model.Protocol, port int) model.Interface {
	return model.Interface{Protocol: p, ListenPort: port, Enabled: true}
}

// Each tunnel is let in on what it is reached by, and on nothing else.
func TestEachTunnelIsLetInOnItsOwnPorts(t *testing.T) {
	tcpOVPN := iface(model.ProtocolOpenVPN, 443)
	tcpOVPN.OpenVPN.V.Transport = "tcp"
	oc := iface(model.ProtocolOpenConnect, 8443)
	oc.OpenConnect.V.DTLS = true
	plain := iface(model.ProtocolL2TP, 41701) // its own port is redirected to 1701 first
	plain.L2TP.V.AllowPlain = true
	off := iface(model.ProtocolWireGuard, 9999)
	off.Enabled = false
	got := ForInterfaces([]model.Interface{
		iface(model.ProtocolWireGuard, 51820), iface(model.ProtocolOpenVPN, 1194), tcpOVPN, oc, plain,
		iface(model.ProtocolIKEv2, 41500), iface(model.ProtocolPPTP, 41723), off,
	})
	var s []string
	for _, r := range got {
		s = append(s, r.String())
	}
	want := "esp gre 443/tcp 1723/tcp 8443/tcp 500/udp 1194/udp 1701/udp 4500/udp 8443/udp 51820/udp"
	if strings.Join(s, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(s, " "), want)
	}
	back, err := Decode(Encode(got))
	if err != nil || len(back) != len(got) {
		t.Fatalf("round trip: %v %v", back, err)
	}
}

// Root acts on the file; one that is not the panel's is refused whole.
func TestAFileThePanelWouldNotWriteIsRefused(t *testing.T) {
	for _, bad := range []string{
		`{"version":2,"open":[]}`,
		`{"version":1,"open":[{"proto":"tcp","port":0}]}`,
		`{"version":1,"open":[{"proto":"tcp","port":70000}]}`,
		`{"version":1,"open":[{"proto":"icmp"}]}`,
		`{"version":1,"open":[{"proto":"gre","port":22}]}`,
		`{"version":1,"open":[{"proto":"tcp; reboot","port":22}]}`,
		`not json`,
	} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("taken: %s", bad)
		}
	}
}

// fake is a firewall in memory; pre is what was in it before the helper.
type fake struct {
	name  string
	rules map[Rule]bool
}

func (f *fake) Name() string { return f.name }
func (f *fake) Allow(r Rule) (bool, error) {
	if f.rules[r] {
		return false, nil
	}
	f.rules[r] = true
	return true, nil
}
func (f *fake) Has(r Rule) (bool, error) { return f.rules[r], nil }
func (f *fake) Remove(r Rule) error      { delete(f.rules, r); return nil }

// What the helper opened it closes when no tunnel wants it; what was there
// before -- the operator's SSH, a port they opened themselves -- it never
// touches, even when a tunnel used it and went.
func TestOnlyTheHelpersOwnRulesAreClosed(t *testing.T) {
	RecordDir = filepath.Join(t.TempDir(), "rec")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ssh, theirs := Rule{"tcp", 22}, Rule{"udp", 51820}
	fw := &fake{name: "ufw", rules: map[Rule]bool{ssh: true, theirs: true}}

	wg, ike := Rule{"udp", 51820}, Rule{"udp", 500}
	if err := Sync([]Rule{wg, ike, {Proto: "esp"}}, []Firewall{fw}, log); err != nil {
		t.Fatal(err)
	}
	if !fw.rules[ike] || !fw.rules[Rule{Proto: "esp"}] || !fw.rules[wg] {
		t.Fatalf("not opened: %v", fw.rules)
	}
	// Something took the helper's rule away: it is put back.
	delete(fw.rules, ike)
	if err := Sync([]Rule{wg, ike, {Proto: "esp"}}, []Firewall{fw}, log); err != nil {
		t.Fatal(err)
	}
	if !fw.rules[ike] {
		t.Fatal("the helper's rule was not put back")
	}
	// Every tunnel gone.
	if err := Sync(nil, []Firewall{fw}, log); err != nil {
		t.Fatal(err)
	}
	if fw.rules[ike] || fw.rules[Rule{Proto: "esp"}] {
		t.Fatalf("the helper's rules stayed: %v", fw.rules)
	}
	if !fw.rules[ssh] || !fw.rules[theirs] {
		t.Fatalf("an operator's rule was closed: %v", fw.rules)
	}
	if info, err := os.Stat(RecordDir); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("the record is not root's alone: %v %v", info, err)
	}
}

// The installer writes the same units.
func TestTheUnitsAreTheInstallersOwn(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(raw), "\r\n", "\n")
	service, watch, err := Units("/usr/local/bin/wui", "/var/lib/wui")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{ServiceUnit: service, PathUnit: watch} {
		re := regexp.MustCompile(`(?s)cat >/etc/systemd/system/` + regexp.QuoteMeta(name) + ` <<HELPER\n(.*?)\nHELPER\n`)
		m := re.FindStringSubmatch(script)
		if m == nil {
			t.Fatalf("install.sh writes no %s", name)
		}
		got := strings.NewReplacer("$BIN_PATH", "/usr/local/bin/wui", "$DATA_DIR", "/var/lib/wui").Replace(m[1] + "\n")
		if got != want {
			t.Errorf("%s differs from the installer's:\n--- here\n%s\n--- install.sh\n%s", name, want, got)
		}
	}
	if _, _, err := Units("/usr/local/bin/wui", "/var/lib/wui dir"); err == nil {
		t.Fatal("a path a unit cannot carry was taken")
	}
}
