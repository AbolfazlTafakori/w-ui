//go:build linux

package l2tpagent

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// Only PPP devices whose local end is the tunnel's gateway are its sessions:
// a PPP uplink the machine also has, or another tunnel's, is not.
func TestSessionsAreThoseOfThisTunnel(t *testing.T) {
	ipJSON := []byte(`[
	 {"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1"}]},
	 {"ifname":"ppp0","addr_info":[{"family":"inet","local":"10.77.0.1","address":"10.77.0.2"}]},
	 {"ifname":"ppp1","addr_info":[{"family":"inet","local":"192.0.2.10","address":"192.0.2.1"}]},
	 {"ifname":"ppp2","addr_info":[{"family":"inet","local":"10.77.0.1","address":"10.77.0.9"}]},
	 {"ifname":"ppp3","addr_info":[{"family":"inet","local":"10.77.0.1","address":"10.77.0.4"}]}
	]`)
	look := func(dev string) (int, int64, string, uint64, uint64) {
		switch dev {
		case "ppp0":
			return 100, 1700000000, "203.0.113.5", 10, 20
		case "ppp2":
			return 102, 1700000100, "", 0, 0
		}
		return 0, 0, "", 0, 0 // ppp3: no pppd found for it
	}
	got, err := sessionsFrom(ipJSON, netip.MustParseAddr("10.77.0.1"), look)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Device != "ppp0" || got[0].IP.String() != "10.77.0.2" || got[0].PID != 100 ||
		got[0].Endpoint != "203.0.113.5" || got[0].RX != 10 || got[1].Device != "ppp2" {
		t.Fatalf("sessions = %+v", got)
	}
}

func TestIpparamReadsTheDevicesPublicAddress(t *testing.T) {
	cmdline := []byte("/usr/sbin/pppd\x00passive\x00nodetach\x00file\x00/run/wui-l2tp/options\x00ipparam\x00203.0.113.5\x00")
	if got := ipparam(cmdline); got != "203.0.113.5" {
		t.Fatalf("ipparam = %q", got)
	}
	if got := ipparam([]byte("pppd\x00ipparam\x00not-an-address\x00")); got != "" {
		t.Fatalf("a value that is not an address was taken: %q", got)
	}
}

// The panel's user owns the L2TP directory and can put a link anywhere in it.
// Nothing it plants makes root read, write or delete outside that directory.
func TestFilesRefuseLinksOutOfTheDirectory(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "shadow")
	if err := os.WriteFile(secret, []byte("root:secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := l2tpconf.NewPaths(data).Dir
	if err := os.MkdirAll(filepath.Join(dir, "kick"), 0o750); err != nil {
		t.Fatal(err)
	}
	// state.json is a link to a file outside.
	if err := os.Symlink(secret, filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	// report.json.tmp and report.json are links to a file outside.
	target := filepath.Join(outside, "victim")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(target, filepath.Join(dir, "report.json.tmp"))
	_ = os.Symlink(target, filepath.Join(dir, "report.json"))
	// A kick request that is a link to a file outside.
	_ = os.Symlink(secret, filepath.Join(dir, "kick", "7"))

	files, done, err := OpenFiles(data, l2tpconf.ProtocolL2TP)
	if err != nil {
		t.Fatal(err)
	}
	defer done()

	if raw, ok, err := files.ReadState(); err == nil || ok || len(raw) != 0 {
		t.Fatalf("a link out was read as the state: ok=%v err=%v raw=%q", ok, err, raw)
	}
	if err := files.WriteReport(l2tpconf.Report{State: l2tpconf.AgentRunning}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if body, _ := os.ReadFile(target); string(body) != "untouched" {
		t.Fatalf("writing the report wrote through a link to %q", body)
	}
	if _, err := files.TakeKicks(); err != nil {
		t.Fatalf("kicks: %v", err)
	}
	if body, err := os.ReadFile(secret); err != nil || string(body) != "root:secret" {
		t.Fatalf("taking a kick request touched the file it linked to: %q %v", body, err)
	}
	report, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil || !strings.Contains(string(report), `"state":"running"`) {
		t.Fatalf("report = %q %v", report, err)
	}
}

// The L2TP directory itself replaced by a link out of the data directory is
// refused; no directory at all is simply no tunnel.
func TestFilesRefuseALinkedDirectory(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, l2tpconf.NewPaths(data).Dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenFiles(data, l2tpconf.ProtocolL2TP); err == nil {
		t.Fatal("a linked L2TP directory was opened")
	}

	empty := t.TempDir()
	files, done, err := OpenFiles(empty, l2tpconf.ProtocolL2TP)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, ok, err := files.ReadState(); ok || err != nil {
		t.Fatalf("no directory read as a state: %v %v", ok, err)
	}
}
