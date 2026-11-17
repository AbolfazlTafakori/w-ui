package l2tpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// fakeIPsec records what the IKEv2 agent asks of the machine.
type fakeIPsec struct {
	calls      []string
	files      []ikev2conf.File
	disk       map[string]string // what WriteSwanctl left, by "dir/name"
	loads      []Loads
	events     []string // each load: "swanctl: ..." or "vici: -unloaded +loaded"
	viciDown   bool
	instance   string // strongSwan's; changed when it starts again
	sas        []IKESA
	down       bool // strongSwan not answering
	terminated []uint
}

func (f *fakeIPsec) EnsurePackages(context.Context, string) error {
	f.calls = append(f.calls, "install")
	return nil
}
func (f *fakeIPsec) WriteSwanctl(files []ikev2conf.File) ([]string, error) {
	f.calls = append(f.calls, "write")
	f.files = files
	if f.disk == nil {
		f.disk = map[string]string{}
	}
	var changed []string
	keep := map[string]bool{}
	for _, file := range files {
		k := file.Dir + "/" + file.Name
		keep[k] = true
		if b, ok := f.disk[k]; !ok || b != file.Body {
			f.disk[k] = file.Body
			changed = append(changed, k)
		}
	}
	for k := range f.disk {
		if !keep[k] {
			delete(f.disk, k)
			changed = append(changed, k)
		}
	}
	return changed, nil
}
func (f *fakeIPsec) LoadSwanctl(_ context.Context, l Loads) error {
	f.calls = append(f.calls, "load")
	f.loads = append(f.loads, l)
	var cmds []string
	for _, args := range swanctlCommands(l) {
		cmds = append(cmds, strings.Join(args, " "))
	}
	f.events = append(f.events, "swanctl: "+strings.Join(cmds, "; "))
	f.down = false
	return nil
}
func (f *fakeIPsec) ApplyItems(_ context.Context, unload, load []ikev2conf.Item) error {
	f.calls = append(f.calls, "apply")
	if f.viciDown {
		f.events = append(f.events, "vici: refused")
		return errors.New("no socket")
	}
	var keys []string
	for _, it := range unload {
		keys = append(keys, "-"+it.Key())
	}
	for _, it := range load {
		keys = append(keys, "+"+it.Key())
	}
	f.events = append(f.events, "vici: "+strings.Join(keys, " "))
	return nil
}
func (f *fakeIPsec) Instance() string { return f.instance }
func (f *fakeIPsec) lastLoad() Loads  { return f.loads[len(f.loads)-1] }
func (f *fakeIPsec) RemoveSwanctl(context.Context) error {
	f.calls = append(f.calls, "remove")
	return nil
}
func (f *fakeIPsec) SAs(context.Context) ([]IKESA, error) {
	if f.down {
		return nil, errors.New("not answering")
	}
	return f.sas, nil
}
func (f *fakeIPsec) Terminate(_ context.Context, id uint) error {
	f.terminated = append(f.terminated, id)
	kept := f.sas[:0]
	for _, s := range f.sas {
		if s.ID != id {
			kept = append(kept, s)
		}
	}
	f.sas = kept
	return nil
}
func (f *fakeIPsec) ServeRadius(string, radiusd.Lookup) error {
	f.calls = append(f.calls, "radius")
	return nil
}
func (f *fakeIPsec) StopRadius() { f.calls = append(f.calls, "radius-stop") }
func (f *fakeIPsec) count(c string) int {
	n := 0
	for _, x := range f.calls {
		if x == c {
			n++
		}
	}
	return n
}

// fakeFiles is the panel's directory: a state, a report, kicks.
type fakeIKEFiles struct {
	state  []byte
	report l2tpconf.Report
	kicks  []uint
}

func (f *fakeIKEFiles) ReadState() ([]byte, bool, error) { return f.state, f.state != nil, nil }
func (f *fakeIKEFiles) WriteReport(r l2tpconf.Report) error {
	f.report = r
	return nil
}
func (f *fakeIKEFiles) RemoveReport() error { return nil }
func (f *fakeIKEFiles) TakeKicks() ([]uint, error) {
	k := f.kicks
	f.kicks = nil
	return k, nil
}

func ikeState(t *testing.T, edit func(*ikev2conf.State)) []byte {
	t.Helper()
	s := &ikev2conf.State{Version: ikev2conf.SchemaVersion, Interface: ikev2conf.Iface{
		ID: 1, Name: "ike0", Subnet: "10.79.0.0/24", Gateway: "10.79.0.1",
		Modes: []string{ikev2conf.ModePSK}, ServerID: "vpn.example.com",
		Accounts: []ikev2conf.Account{
			{ID: 1, Username: "s1-laptop", Secret: "Laptop123", IP: "10.79.0.2"},
			{ID: 2, Username: "s1-phone", Secret: "Phone1234", IP: "10.79.0.3"},
		},
	}}
	if edit != nil {
		edit(s)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ikeRig(t *testing.T) (*IKEv2Agent, *fakeIPsec, *fakeIKEFiles) {
	sys := &fakeIPsec{down: true}
	files := &fakeIKEFiles{state: ikeState(t, nil)}
	a := NewIKEv2(sys, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.now = func() time.Time { return time.Unix(1_000_000, 0) }
	return a, sys, files
}

func sa(id uint, account uint, age int64) IKESA {
	return IKESA{Conn: ikev2conf.ConnName(account, ikev2conf.ModePSK), ID: id, Established: true, Age: age,
		RemoteHost: "198.51.100.7", VIP: netip.MustParseAddr("10.79.0.2"), BytesIn: 100, BytesOut: 50}
}

// The first pass installs, writes and loads; an unchanged state does neither
// again; a changed one writes and loads.
func TestTheIKEv2TunnelIsLoadedOnceAndAgainWhenItChanges(t *testing.T) {
	a, sys, files := ikeRig(t)
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("install") != 1 || sys.count("write") != 1 || sys.count("load") != 1 {
		t.Fatalf("first pass: %v", sys.calls)
	}
	if files.report.State != l2tpconf.AgentRunning {
		t.Fatalf("report %+v", files.report)
	}
	_ = a.Step(context.Background())
	if sys.count("write") != 1 {
		t.Fatalf("an unchanged state was written again: %v", sys.calls)
	}
	files.state = ikeState(t, func(s *ikev2conf.State) { s.Interface.Accounts = s.Interface.Accounts[:1] })
	_ = a.Step(context.Background()) // seen; loaded once the panel has stopped changing it
	_ = a.Step(context.Background())
	if sys.count("write") != 2 || sys.count("apply") != 1 || sys.count("load") != 1 {
		t.Fatalf("a changed state was not loaded alone: %v", sys.calls)
	}
	// strongSwan dies: the next pass loads it all again.
	sys.down = true
	_ = a.Step(context.Background())
	if sys.count("load") != 2 || !sys.lastLoad().All {
		t.Fatalf("strongSwan down was not reloaded: %v %+v", sys.calls, sys.loads)
	}
}

// settle steps until the change in files.state is loaded, as Run does, and
// returns how it was: one load, through swanctl or over the socket, or
// both.
func settle(t *testing.T, a *IKEv2Agent, sys *fakeIPsec) string {
	t.Helper()
	n := len(sys.events)
	for i := 0; i < 2; i++ {
		if err := a.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(sys.events[n:], " | ")
}

// testPKI is a tunnel's authority, the server's certificate (and a renewed
// one) and every test device's, made once.
var testPKI = sync.OnceValue(func() map[string]string {
	ca, err := ikev2conf.NewAuthority("ike0")
	if err != nil {
		panic(err)
	}
	out := map[string]string{"ca": ca.Cert}
	for _, k := range []string{"server", "server2"} {
		srv, err := ca.IssueServer([]string{"vpn.example.com"})
		if err != nil {
			panic(err)
		}
		out[k], out[k+"-key"] = srv.Cert, srv.Key
	}
	for _, u := range []string{"s1-laptop", "s1-phone", "s1-tab", "s1-tab2"} {
		d, err := ca.IssueDevice(strings.TrimSuffix(u, "2"))
		if err != nil {
			panic(err)
		}
		out[u] = d.Cert
	}
	return out
})

// withCerts turns certificate sign-ins on beside PSK.
func withCerts(s *ikev2conf.State) {
	p := testPKI()
	s.Interface.Modes = []string{ikev2conf.ModePSK, ikev2conf.ModeRSA}
	s.Interface.CACert, s.Interface.ServerCert, s.Interface.ServerKey = p["ca"], p["server"], p["server-key"]
	for i := range s.Interface.Accounts {
		s.Interface.Accounts[i].Cert = p[s.Interface.Accounts[i].Username]
	}
}

// One change has strongSwan load only what it touched -- one device's
// connections, pool, secret, over the socket -- and never everything, nor
// its own settings.
func TestOneChangeLoadsOnlyWhatChanged(t *testing.T) {
	a, sys, files := ikeRig(t)
	tab := ikev2conf.Account{ID: 3, Username: "s1-tab", Secret: "Tablet123", IP: "10.79.0.4"}
	state := func(edit func(s *ikev2conf.State)) []byte {
		return ikeState(t, func(s *ikev2conf.State) {
			s.Interface.Accounts = append(s.Interface.Accounts, tab)
			withCerts(s)
			if edit != nil {
				edit(s)
			}
		})
	}
	files.state = ikeState(t, withCerts)
	_ = a.Step(context.Background())
	if l := sys.lastLoad(); !l.All {
		t.Fatalf("first pass of a run: %+v", l)
	}
	renewed := func(s *ikev2conf.State) { s.Interface.Accounts[2].Cert = testPKI()["s1-tab2"] }
	rotated := func(s *ikev2conf.State) { renewed(s); s.Interface.Accounts[2].Secret = "Rotated99" }
	newServer := func(s *ikev2conf.State) {
		rotated(s)
		s.Interface.ServerCert, s.Interface.ServerKey = testPKI()["server2"], testPKI()["server2-key"]
	}
	for _, c := range []struct {
		what string
		edit func(s *ikev2conf.State)
		want string
	}{
		{"a device added: its connections, pool and secret", nil,
			"vici: +conn:wui-ikev2-3-psk +conn:wui-ikev2-3-rsa +pool:wui-ikev2-3 +secret:ike-wui-ikev2-3"},
		{"its certificate renewed: the connection pinning it", renewed, "vici: +conn:wui-ikev2-3-rsa"},
		{"its password changed: its secret", rotated, "vici: +secret:ike-wui-ikev2-3"},
		{"the server's certificate renewed: the credentials, and the connections that send it", newServer,
			"vici: +conn:wui-ikev2-1-rsa +conn:wui-ikev2-2-rsa +conn:wui-ikev2-3-rsa | swanctl: --load-creds --noprompt"},
		{"nothing changed", newServer, ""},
	} {
		files.state = state(c.edit)
		if got := settle(t, a, sys); got != c.want {
			t.Fatalf("%s:\n got %s\nwant %s", c.what, got, c.want)
		}
	}
	for _, l := range sys.loads[1:] {
		if l.All || l.Settings {
			t.Fatalf("a change loaded everything or the settings: %+v", sys.loads)
		}
	}
}

// A device taken away loses its way in: its session is ended before
// anything is loaded, then its connections, pool and secret are unloaded,
// and its files are gone.
func TestARemovedDeviceIsUnloaded(t *testing.T) {
	a, sys, files := ikeRig(t)
	files.state = ikeState(t, withCerts)
	_ = a.Step(context.Background())
	sys.sas = []IKESA{sa(21, 2, 10)}
	sys.calls = nil
	files.state = ikeState(t, func(s *ikev2conf.State) { s.Interface.Accounts = s.Interface.Accounts[:1]; withCerts(s) })
	_ = a.Step(context.Background())
	if len(sys.terminated) != 1 || sys.terminated[0] != 21 || sys.count("apply") != 0 {
		t.Fatalf("its session was not ended before the load: ended %v, calls %v", sys.terminated, sys.calls)
	}
	want := "vici: -conn:wui-ikev2-2-psk -conn:wui-ikev2-2-rsa -pool:wui-ikev2-2 -secret:ike-wui-ikev2-2"
	if got := settle(t, a, sys); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for k, body := range sys.disk {
		if strings.Contains(k, "wui-ikev2-2.pem") || strings.Contains(body, "s1-phone") || strings.Contains(body, "wui-ikev2-2 ") {
			t.Fatalf("%s still names the device", k)
		}
	}
}

// strongSwan's socket refusing, the change is loaded through swanctl: every
// part it touched, from the files.
func TestWithoutTheSocketSwanctlLoadsTheChange(t *testing.T) {
	a, sys, files := ikeRig(t)
	_ = a.Step(context.Background())
	sys.viciDown = true
	files.state = ikeState(t, func(s *ikev2conf.State) { s.Interface.Accounts = s.Interface.Accounts[:1] })
	want := "vici: refused | swanctl: --load-creds --noprompt; --load-pools; --load-conns"
	if got := settle(t, a, sys); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Changes coming one on another -- the panel making thousands of
// certificates -- are loaded together, never more than once in settleFor,
// and each device once, whatever batch it came in.
func TestChangesInARowAreLoadedTogether(t *testing.T) {
	a, sys, files := ikeRig(t)
	now := time.Unix(1_000_000, 0)
	a.now = func() time.Time { return now }
	_ = a.Step(context.Background())
	n := len(sys.events)
	for i := 0; i < 60; i++ { // a minute, a new device every second
		last := 10 + i
		files.state = ikeState(t, func(s *ikev2conf.State) {
			for j := 10; j <= last; j++ {
				s.Interface.Accounts = append(s.Interface.Accounts, ikev2conf.Account{
					ID: uint(j), Username: "s1-d" + strconv.Itoa(j), Secret: "Secret123", IP: "10.79.0." + strconv.Itoa(j)})
			}
		})
		_ = a.Step(context.Background())
		now = now.Add(time.Second)
	}
	if got := len(sys.events) - n; got < 5 || got > 7 {
		t.Fatalf("%d loads for 60 changes in a minute", got)
	}
	_ = a.Step(context.Background())
	if a.applied != filesFingerprint(sys.files) || a.applied == "" {
		t.Fatal("the last change was not loaded once the panel stopped")
	}
	loaded := map[string]int{}
	for _, e := range sys.events[n:] {
		for _, k := range strings.Fields(strings.TrimPrefix(e, "vici: ")) {
			loaded[k]++
		}
	}
	for k, c := range loaded {
		if c != 1 {
			t.Fatalf("%s loaded %d times", k, c)
		}
	}
	if len(loaded) != 60*3 {
		t.Fatalf("%d items loaded, want 180", len(loaded))
	}
}

func TestSwanctlCommands(t *testing.T) {
	for _, c := range []struct {
		l    Loads
		want string
	}{
		{Loads{}, ""},
		{Loads{All: true}, "--load-all --noprompt"},
		{Loads{All: true, Settings: true}, "--load-all --noprompt; --reload-settings"},
		{Loads{Creds: true}, "--load-creds --noprompt"},
		{Loads{Secrets: true}, "--load-creds --noprompt"},
		{Loads{Conns: true}, "--load-conns"},
		{Loads{Pools: true, Conns: true}, "--load-pools; --load-conns"},
		{Loads{Creds: true, Pools: true, Conns: true, Settings: true}, "--load-creds --noprompt; --load-pools; --load-conns; --reload-settings"},
	} {
		var parts []string
		for _, args := range swanctlCommands(c.l) {
			parts = append(parts, strings.Join(args, " "))
		}
		if got := strings.Join(parts, "; "); got != c.want {
			t.Errorf("%+v: %q, want %q", c.l, got, c.want)
		}
	}
	for _, c := range []struct {
		changed []string
		want    Loads
	}{
		{[]string{"strongswan.d/wui-ikev2-radius.conf"}, Loads{Settings: true}},
		{[]string{"x509ca/wui-ikev2-ca.pem", "private/wui-ikev2-server.pem"}, Loads{Creds: true}},
		{[]string{"conf.d/wui-ikev2-secrets.conf"}, Loads{Secrets: true}},
		{[]string{"conf.d/wui-ikev2-pools.conf"}, Loads{Pools: true}},
		{[]string{"conf.d/wui-ikev2.conf", "wui-ikev2-certs/wui-ikev2-7.pem"}, Loads{Conns: true}},
		// From before the device certificates moved: taken out of x509.
		{[]string{"x509/wui-ikev2-7.pem"}, Loads{Creds: true}},
	} {
		if got := loadsFor(c.changed); got != c.want {
			t.Errorf("%v: %+v, want %+v", c.changed, got, c.want)
		}
	}
}

// strongSwan killed and started again by systemd answers at once, holding
// who knows what: the agent notices, loads everything once, and goes back
// to loading only what changes.
func TestAStrongSwanStartedAgainIsLoadedOnce(t *testing.T) {
	a, sys, _ := ikeRig(t)
	sys.instance = "1"
	_ = a.Step(context.Background())
	n := len(sys.loads)
	_ = a.Step(context.Background())
	if len(sys.loads) != n {
		t.Fatal("loaded with nothing changed")
	}
	sys.instance = "2"
	_ = a.Step(context.Background())
	if len(sys.loads) != n+1 || !sys.lastLoad().All {
		t.Fatalf("a restarted strongSwan was not loaded: %+v", sys.loads[n:])
	}
	_ = a.Step(context.Background())
	_ = a.Step(context.Background())
	if len(sys.loads) != n+1 {
		t.Fatalf("loaded again: %+v", sys.loads[n:])
	}
}

// The agent restarted, strongSwan still running, its files already as they
// should be: everything is loaded once, so what it holds matches the files.
func TestARestartedAgentLoadsEverythingOnce(t *testing.T) {
	a, sys, files := ikeRig(t)
	_ = a.Step(context.Background())
	b := NewIKEv2(sys, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = b.Step(context.Background())
	if l := sys.lastLoad(); len(sys.loads) != 2 || !l.All {
		t.Fatalf("loads %+v", sys.loads)
	}
	_ = b.Step(context.Background())
	if len(sys.loads) != 2 {
		t.Fatalf("loaded again with nothing changed: %+v", sys.loads)
	}
}

// Sessions that should not exist are ended: a device taken off, one kicked,
// one whose password changed, and all but the newest of one device's.
func TestIKEv2SessionsThatShouldNotExistAreEnded(t *testing.T) {
	a, sys, files := ikeRig(t)
	sys.sas = []IKESA{sa(11, 1, 30), sa(12, 2, 20), {Conn: "wui-l2tp", ID: 99, Established: true}}
	_ = a.Step(context.Background())
	if len(sys.terminated) != 0 {
		t.Fatalf("ended %v", sys.terminated)
	}
	if got := files.report.Sessions; len(got) != 2 || got[0].Account != 1 || got[0].Endpoint != "198.51.100.7" ||
		got[0].Since != 1_000_000-30 || got[0].RX != 100 || got[0].TX != 50 {
		t.Fatalf("report %+v", got)
	}

	// The phone signs in again from somewhere else: the newer stays.
	sys.sas = append(sys.sas, sa(13, 2, 2))
	_ = a.Step(context.Background())
	if len(sys.terminated) != 1 || sys.terminated[0] != 12 {
		t.Fatalf("ended %v, want the older phone session", sys.terminated)
	}

	// The laptop's password changes.
	files.state = ikeState(t, func(s *ikev2conf.State) { s.Interface.Accounts[0].Secret = "Rotated99" })
	_ = a.Step(context.Background())
	if sys.terminated[len(sys.terminated)-1] != 11 {
		t.Fatalf("ended %v, want the laptop after its password changed", sys.terminated)
	}

	// The panel asks for the phone to go, and then takes it off.
	files.kicks = []uint{2}
	_ = a.Step(context.Background())
	if sys.terminated[len(sys.terminated)-1] != 13 {
		t.Fatalf("ended %v, want the phone on request", sys.terminated)
	}
	sys.sas = append(sys.sas, sa(14, 2, 1))
	files.state = ikeState(t, func(s *ikev2conf.State) { s.Interface.Accounts = s.Interface.Accounts[:1] })
	_ = a.Step(context.Background())
	if sys.terminated[len(sys.terminated)-1] != 14 {
		t.Fatalf("ended %v, want the phone taken off the tunnel", sys.terminated)
	}
	// L2TP's session is never touched.
	for _, id := range sys.terminated {
		if id == 99 {
			t.Fatal("ended L2TP's session")
		}
	}
}

func TestNoIKEv2StateTakesItAway(t *testing.T) {
	a, sys, files := ikeRig(t)
	_ = a.Step(context.Background())
	files.state = nil
	if err := a.Step(context.Background()); !errors.Is(err, ErrNoTunnel) {
		t.Fatalf("err %v", err)
	}
	if sys.count("remove") != 1 {
		t.Fatalf("not taken away: %v", sys.calls)
	}
	_ = a.Step(context.Background())
	if sys.count("remove") != 1 {
		t.Fatal("taken away twice")
	}
}

func TestARefusedIKEv2StateChangesNothing(t *testing.T) {
	a, sys, files := ikeRig(t)
	files.state = []byte(`{"version":1,"interface":{"id":1,"name":"bad name"}}`)
	if err := a.Step(context.Background()); err == nil {
		t.Fatal("accepted")
	}
	if sys.count("write") != 0 || files.report.State != l2tpconf.AgentFailed {
		t.Fatalf("calls %v report %+v", sys.calls, files.report)
	}
}
