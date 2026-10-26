package l2tpagent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

type fakeSys struct {
	calls        []string
	secrets      *l2tpconf.State
	run          map[string]string
	ipsec        string
	firewall     string
	running      bool
	installErr   error
	sessions     []Live
	ended        []int
	gatewayAsked netip.Addr
}

func (f *fakeSys) log(c string) { f.calls = append(f.calls, c) }

func (f *fakeSys) EnsurePackages(context.Context, string) error {
	f.log("install")
	return f.installErr
}
func (f *fakeSys) WriteSecrets(_ string, s *l2tpconf.State) error {
	f.log("secrets")
	f.secrets = s
	return nil
}
func (f *fakeSys) WriteRun(name, body string) error {
	f.log("run:" + name)
	if f.run == nil {
		f.run = map[string]string{}
	}
	f.run[name] = body
	return nil
}
func (f *fakeSys) RemoveRun(...string) error { f.log("rmrun"); f.run = nil; return nil }
func (f *fakeSys) LoadIPsec(_ context.Context, conf string) error {
	f.log("ipsec")
	f.ipsec = conf
	return nil
}
func (f *fakeSys) UnloadIPsec(context.Context) error { f.log("unipsec"); f.ipsec = ""; return nil }
func (f *fakeSys) ApplyFirewall(_ context.Context, script string) error {
	f.log("firewall")
	f.firewall = script
	return nil
}
func (f *fakeSys) Start(context.Context, string) error { f.log("start"); f.running = true; return nil }
func (f *fakeSys) Stop(context.Context, string) error  { f.log("stop"); f.running = false; return nil }
func (f *fakeSys) Running(string) bool                 { return f.running }
func (f *fakeSys) Sessions(gw netip.Addr) ([]Live, error) {
	f.gatewayAsked = gw
	out := append([]Live(nil), f.sessions...)
	return out, nil
}
func (f *fakeSys) EndSession(pid int) error {
	f.ended = append(f.ended, pid)
	kept := f.sessions[:0]
	for _, s := range f.sessions {
		if s.PID != pid {
			kept = append(kept, s)
		}
	}
	f.sessions = kept
	return nil
}

func (f *fakeSys) count(c string) int {
	n := 0
	for _, x := range f.calls {
		if x == c {
			n++
		}
	}
	return n
}

type fakeFiles struct {
	state   []byte
	report  l2tpconf.Report
	reports int
	kicks   []uint
	removed bool
}

func (f *fakeFiles) ReadState() ([]byte, bool, error) {
	if f.state == nil {
		return nil, false, nil
	}
	return f.state, true, nil
}
func (f *fakeFiles) WriteReport(r l2tpconf.Report) error { f.report = r; f.reports++; return nil }
func (f *fakeFiles) RemoveReport() error                 { f.removed = true; return nil }
func (f *fakeFiles) TakeKicks() ([]uint, error) {
	k := f.kicks
	f.kicks = nil
	return k, nil
}

func state(mutate ...func(*l2tpconf.State)) []byte {
	s := &l2tpconf.State{
		Version: l2tpconf.SchemaVersion,
		Interface: l2tpconf.Iface{
			ID: 4, Name: "l2tp0", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1",
			DNS: []string{"1.1.1.1"}, MTU: 1400, PSK: "SharedKey123",
			Accounts: []l2tpconf.Account{
				{ID: 1, Username: "s9-laptop", Secret: "Secret11", IP: "10.77.0.2"},
				{ID: 2, Username: "s9-phone", Secret: "Secret99", IP: "10.77.0.3"},
			},
		},
	}
	for _, m := range mutate {
		m(s)
	}
	raw, err := l2tpconf.Encode(s)
	if err != nil {
		panic(err)
	}
	return raw
}

func rig() (*Agent, *fakeSys, *fakeFiles) {
	sys := &fakeSys{}
	files := &fakeFiles{state: state()}
	a := New(l2tpconf.ProtocolL2TP, sys, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.now = func() time.Time { return time.Unix(1700000000, 0) }
	return a, sys, files
}

func session(pid int, ip string) Live {
	return Live{Session: l2tpconf.Session{Device: "ppp" + ip[len(ip)-1:], Since: int64(pid)}, IP: netip.MustParseAddr(ip), PID: pid}
}

func TestTheFirstStepBringsTheTunnelUp(t *testing.T) {
	a, sys, files := rig()
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"install", "run:options", "run:xl2tpd.conf", "run:l2tp-secrets", "secrets", "ipsec", "firewall", "start"} {
		if sys.count(c) != 1 {
			t.Errorf("%s ran %d times, want once (calls %v)", c, sys.count(c), sys.calls)
		}
	}
	if !strings.Contains(sys.ipsec, "SharedKey123") || !strings.Contains(sys.firewall, "meta ipsec missing drop") {
		t.Errorf("ipsec %q firewall %q", sys.ipsec, sys.firewall)
	}
	if files.report.State != l2tpconf.AgentRunning || files.report.Applied == "" {
		t.Errorf("report = %+v", files.report)
	}
	if sys.gatewayAsked.String() != "10.77.0.1" {
		t.Errorf("sessions asked for %v", sys.gatewayAsked)
	}
}

// An account added or removed changes the credentials pppd reads at sign-in
// and nothing else: no restart, so nobody else on the tunnel is disconnected.
func TestAnAccountChangeRestartsNothing(t *testing.T) {
	a, sys, files := rig()
	_ = a.Step(context.Background())
	files.state = state(func(s *l2tpconf.State) {
		s.Interface.Accounts = append(s.Interface.Accounts, l2tpconf.Account{ID: 3, Username: "s9-tablet", Secret: "Secret33", IP: "10.77.0.4"})
	})
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("secrets") != 2 || sys.count("stop") != 0 || sys.count("ipsec") != 1 || sys.count("start") != 1 {
		t.Fatalf("calls %v", sys.calls)
	}
	if len(sys.secrets.Interface.Accounts) != 3 {
		t.Fatalf("secrets carry %d accounts", len(sys.secrets.Interface.Accounts))
	}
	// And a pass with nothing changed writes nothing at all.
	_ = a.Step(context.Background())
	if sys.count("secrets") != 2 {
		t.Fatalf("an unchanged state rewrote the credentials: %v", sys.calls)
	}
}

// A change to the tunnel itself -- its resolver here -- is read by xl2tpd and
// pppd at start, so xl2tpd is restarted.
func TestATunnelChangeRestartsXl2tpd(t *testing.T) {
	a, sys, files := rig()
	_ = a.Step(context.Background())
	files.state = state(func(s *l2tpconf.State) { s.Interface.DNS = []string{"9.9.9.9"} })
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("stop") != 1 || sys.count("start") != 2 || !strings.Contains(sys.run["options"], "ms-dns 9.9.9.9") {
		t.Fatalf("calls %v options %q", sys.calls, sys.run["options"])
	}
}

// pppd checks credentials at sign-in only. A device taken off the tunnel, one
// whose password changed, one on an address no account holds, and one the
// panel asked to disconnect are all ended; everyone else stays.
func TestSessionsThatShouldNotExistAreEnded(t *testing.T) {
	a, sys, files := rig()
	sys.sessions = []Live{session(101, "10.77.0.2"), session(102, "10.77.0.3"), session(103, "10.77.0.9")}
	_ = a.Step(context.Background())
	if len(sys.ended) != 1 || sys.ended[0] != 103 {
		t.Fatalf("ended %v, want only the unknown address", sys.ended)
	}
	if got := files.report.Sessions; len(got) != 2 || got[0].Account != 1 || got[1].Account != 2 {
		t.Fatalf("report sessions %+v", got)
	}

	// The phone's password changes: its session ends, the laptop's stays.
	files.state = state(func(s *l2tpconf.State) { s.Interface.Accounts[1].Secret = "Rotated99" })
	_ = a.Step(context.Background())
	if len(sys.ended) != 2 || sys.ended[1] != 102 {
		t.Fatalf("ended %v, want the phone after its password changed", sys.ended)
	}

	// The panel asks for the laptop to be disconnected.
	files.kicks = []uint{1}
	_ = a.Step(context.Background())
	if len(sys.ended) != 3 || sys.ended[2] != 101 {
		t.Fatalf("ended %v, want the laptop on request", sys.ended)
	}

	// The laptop signs in again and is then taken off the tunnel.
	sys.sessions = []Live{session(201, "10.77.0.2")}
	_ = a.Step(context.Background())
	files.state = state(func(s *l2tpconf.State) { s.Interface.Accounts = s.Interface.Accounts[1:] })
	_ = a.Step(context.Background())
	if sys.ended[len(sys.ended)-1] != 201 {
		t.Fatalf("ended %v, want the removed laptop", sys.ended)
	}
}

// One sign-in, one session: a second device -- or the same one back from
// another network -- replaces the session before it, as OpenVPN does, and
// other accounts' sessions are not touched.
func TestASecondSignInReplacesTheFirst(t *testing.T) {
	a, sys, files := rig()
	sys.sessions = []Live{session(101, "10.77.0.2"), session(102, "10.77.0.3")}
	_ = a.Step(context.Background())
	if len(sys.ended) != 0 {
		t.Fatalf("ended %v with one session each", sys.ended)
	}

	// The laptop's sign-in is used again, on a second link.
	again := session(150, "10.77.0.2")
	again.Device = "ppp7"
	sys.sessions = append(sys.sessions, again)
	_ = a.Step(context.Background())
	if len(sys.ended) != 1 || sys.ended[0] != 101 {
		t.Fatalf("ended %v, want the older laptop session", sys.ended)
	}
	if got := files.report.Sessions; len(got) != 2 {
		t.Fatalf("report sessions %+v, want the phone and the new laptop session", got)
	}
	for _, s := range files.report.Sessions {
		if s.Account == 1 && s.Device != "ppp7" {
			t.Fatalf("the laptop is reported on %s, not its new link", s.Device)
		}
	}

	// Two that started in the same second: the later pppd stays.
	twin := session(160, "10.77.0.3")
	twin.Since = 102
	twin.Device = "ppp8"
	sys.sessions = append(sys.sessions, twin)
	_ = a.Step(context.Background())
	if sys.ended[len(sys.ended)-1] != 102 {
		t.Fatalf("ended %v, want the earlier pppd of the same second", sys.ended)
	}
}

// A state that does not validate is reported and not carried out: what is
// running keeps running.
func TestARefusedStateChangesNothing(t *testing.T) {
	a, sys, files := rig()
	_ = a.Step(context.Background())
	before := len(sys.calls)
	files.state = state()
	files.state = []byte(strings.Replace(string(files.state), `"Secret11"`, `"bad\"pw\"x"`, 1))
	if err := a.Step(context.Background()); err == nil {
		t.Fatal("a refused state was not reported as an error")
	}
	if len(sys.calls) != before {
		t.Fatalf("a refused state touched the system: %v", sys.calls[before:])
	}
	if files.report.State != l2tpconf.AgentFailed || files.report.Error == "" {
		t.Fatalf("report %+v", files.report)
	}
}

// Packages that will not install are reported, and tried again next pass.
func TestAFailedInstallIsRetried(t *testing.T) {
	a, sys, files := rig()
	sys.installErr = errors.New("apt is locked")
	if err := a.Step(context.Background()); err == nil || files.report.State != l2tpconf.AgentFailed {
		t.Fatalf("report %+v", files.report)
	}
	if sys.count("start") != 0 {
		t.Fatal("started without the packages")
	}
	sys.installErr = nil
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("install") != 2 || sys.count("start") != 1 {
		t.Fatalf("calls %v", sys.calls)
	}
}

// No state file: the panel has no L2TP tunnel here. Everything is taken down
// and the run ends, so nothing keeps holding a port.
func TestNoStateTakesEverythingDown(t *testing.T) {
	a, sys, files := rig()
	_ = a.Step(context.Background())
	files.state = nil
	if err := a.Step(context.Background()); !errors.Is(err, ErrNoTunnel) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range []string{"stop", "unipsec", "rmrun"} {
		if sys.count(c) != 1 {
			t.Errorf("%s ran %d times", c, sys.count(c))
		}
	}
	if sys.secrets != nil || !strings.Contains(sys.firewall, "delete table inet wui_l2tp") || !files.removed {
		t.Errorf("secrets %v firewall %q report removed %v", sys.secrets, sys.firewall, files.removed)
	}
}

// A new run adopts the xl2tpd an earlier one left running rather than
// starting another, so restarting the agent disconnects nobody.
func TestANewRunAdoptsTheRunningXl2tpd(t *testing.T) {
	a, sys, _ := rig()
	sys.running = true
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("start") != 0 || sys.count("stop") != 0 {
		t.Fatalf("calls %v", sys.calls)
	}
}

// xl2tpd answers from the endpoint that is this server's own address, when
// exactly one is; with none or several, or with a relay in front of this
// server among them, it listens on every address.
func TestXl2tpdAnswersFromTheAddressDevicesDial(t *testing.T) {
	local := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.5"),
		netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("203.0.113.8")}
	names := map[string][]netip.Addr{
		"vpn.example.com": {netip.MustParseAddr("203.0.113.7")},
		"other.example":   {netip.MustParseAddr("198.51.100.1")},
	}
	lookup := func(h string) []netip.Addr { return names[h] }
	for _, c := range []struct {
		endpoints []string
		want      string
	}{
		{[]string{"vpn.example.com"}, "203.0.113.7"},
		{[]string{"203.0.113.8"}, "203.0.113.8"},
		{[]string{"vpn.example.com", "203.0.113.7"}, "203.0.113.7"},
		{[]string{"vpn.example.com", "203.0.113.8"}, ""},
		{[]string{"other.example"}, ""},
		{[]string{"203.0.113.7", "other.example"}, ""}, // a relay forwards here too
		{[]string{"203.0.113.7", "198.51.100.1"}, ""},  // the relay by address
		{[]string{"203.0.113.7", "unresolved.example"}, "203.0.113.7"},
		{[]string{"127.0.0.1"}, ""},
		{nil, ""},
	} {
		if got := pickListen(c.endpoints, local, lookup); got != c.want {
			t.Errorf("%v: %q, want %q", c.endpoints, got, c.want)
		}
	}
	s := &l2tpconf.State{Interface: l2tpconf.Iface{ListenAddr: "203.0.113.7"}}
	if !strings.Contains(l2tpconf.XL2TPD(s), "listen-addr = 203.0.113.7\n") {
		t.Fatal("xl2tpd is not bound to it")
	}
	s.Interface.ListenAddr = ""
	if strings.Contains(l2tpconf.XL2TPD(s), "listen-addr") {
		t.Fatal("bound with no address")
	}
}
