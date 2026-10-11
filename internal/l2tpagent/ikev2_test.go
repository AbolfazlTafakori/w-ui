package l2tpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// fakeIPsec records what the IKEv2 agent asks of the machine.
type fakeIPsec struct {
	calls      []string
	files      []ikev2conf.File
	sas        []IKESA
	down       bool // strongSwan not answering
	terminated []uint
}

func (f *fakeIPsec) EnsurePackages(context.Context, string) error {
	f.calls = append(f.calls, "install")
	return nil
}
func (f *fakeIPsec) WriteSwanctl(files []ikev2conf.File) error {
	f.calls = append(f.calls, "write")
	f.files = files
	return nil
}
func (f *fakeIPsec) LoadSwanctl(context.Context) error {
	f.calls = append(f.calls, "load")
	f.down = false
	return nil
}
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
	_ = a.Step(context.Background())
	if sys.count("write") != 2 || sys.count("load") != 2 {
		t.Fatalf("a changed state was not loaded: %v", sys.calls)
	}
	// strongSwan dies: the next pass loads it all again.
	sys.down = true
	_ = a.Step(context.Background())
	if sys.count("load") != 3 {
		t.Fatalf("strongSwan down was not reloaded: %v", sys.calls)
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
