package l2tpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// fakeOC records what the OpenConnect agent asks of the machine.
type fakeOC struct {
	calls      []string
	files      map[string]string
	running    bool
	down       bool // ocserv not answering
	users      []OCUser
	ended      []int
	camouflage bool
	lookup     radiusd.Lookup
	onAccept   radiusd.OnAccept
	secret     string
}

func (f *fakeOC) EnsurePackages(context.Context, string) error {
	f.calls = append(f.calls, "install")
	return nil
}
func (f *fakeOC) WriteOcserv(files []occonf.File) (bool, bool, error) {
	changed, onlyCert := false, true
	if f.files == nil {
		f.files = map[string]string{}
	}
	for _, x := range files {
		if f.files[x.Name] == x.Body {
			continue
		}
		changed = true
		if x.Name != occonf.CertFile && x.Name != occonf.KeyFile {
			onlyCert = false
		}
		f.files[x.Name] = x.Body
	}
	return changed, changed && onlyCert, nil
}
func (f *fakeOC) RadiusSecret() string { return f.secret }
func (f *fakeOC) OcservRunning(context.Context) bool {
	return f.running
}
func (f *fakeOC) StartOcserv(context.Context) error {
	f.calls = append(f.calls, "start")
	f.running, f.down = true, false
	return nil
}
func (f *fakeOC) RestartOcserv(context.Context) error {
	f.calls = append(f.calls, "restart")
	f.users = nil
	return nil
}
func (f *fakeOC) ReloadOcserv(context.Context) error {
	f.calls = append(f.calls, "reload")
	return nil
}
func (f *fakeOC) RemoveOcserv(context.Context) error {
	f.calls = append(f.calls, "remove")
	f.running = false
	return nil
}
func (f *fakeOC) OcservUsers(context.Context) ([]OCUser, error) {
	if f.down {
		return nil, errors.New("not answering")
	}
	return append([]OCUser(nil), f.users...), nil
}
func (f *fakeOC) Disconnect(_ context.Context, id int) error {
	f.ended = append(f.ended, id)
	kept := f.users[:0]
	for _, u := range f.users {
		if u.ID != id {
			kept = append(kept, u)
		}
	}
	f.users = kept
	return nil
}
func (f *fakeOC) Camouflage(context.Context) bool { return f.camouflage }
func (f *fakeOC) ServeOCRadius(secret string, l radiusd.Lookup, on radiusd.OnAccept) error {
	f.secret, f.lookup, f.onAccept = secret, l, on
	return nil
}
func (f *fakeOC) StopOCRadius() { f.calls = append(f.calls, "radius-stop") }
func (f *fakeOC) count(c string) int {
	n := 0
	for _, x := range f.calls {
		if x == c {
			n++
		}
	}
	return n
}

// ocCert is one certificate for every state of a test, so a change made on
// purpose is the only change.
var ocCert, ocKey = func() (string, string) {
	c, k, err := occonf.NewServerCert([]string{"vpn.example.com"})
	if err != nil {
		panic(err)
	}
	return c, k
}()

func ocState(t *testing.T, edit func(*occonf.State)) []byte {
	t.Helper()
	cert, key := ocCert, ocKey
	s := &occonf.State{Version: occonf.SchemaVersion, Interface: occonf.Iface{
		ID: 1, Name: "oc0", Port: 443, Subnet: "10.81.0.0/24", Gateway: "10.81.0.1",
		DNS: []string{"1.1.1.1"}, MTU: occonf.DefaultMTU, DTLS: true, Cert: cert, Key: key,
		Accounts: []occonf.Account{
			{ID: 1, Username: "s1-laptop", Secret: "Laptop123", IP: "10.81.0.2"},
			{ID: 2, Username: "s1-phone", Secret: "Phone1234", IP: "10.81.0.3"},
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

func ocRig(t *testing.T) (*OpenConnectAgent, *fakeOC, *fakeIKEFiles) {
	sys := &fakeOC{}
	files := &fakeIKEFiles{state: ocState(t, nil)}
	a := NewOpenConnect(sys, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.now = func() time.Time { return time.Unix(1_000_000, 0) }
	return a, sys, files
}

// The first pass installs, writes and starts; an unchanged state does
// nothing to ocserv; a new certificate alone is read again without ending
// anyone; any other change restarts it.
func TestOcservIsStartedOnceAndRestartedOnlyForWhatNeedsIt(t *testing.T) {
	a, sys, files := ocRig(t)
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.count("install") != 1 || sys.count("start") != 1 || files.report.State != l2tpconf.AgentRunning {
		t.Fatalf("first pass: %v %+v", sys.calls, files.report)
	}
	_ = a.Step(context.Background())
	if sys.count("start")+sys.count("restart")+sys.count("reload") != 1 {
		t.Fatalf("an unchanged state touched ocserv: %v", sys.calls)
	}
	// A device added changes no file: ocserv is not touched, RADIUS knows it.
	files.state = ocState(t, func(s *occonf.State) {
		s.Interface.Accounts = append(s.Interface.Accounts, occonf.Account{ID: 3, Username: "s1-tv", Secret: "Television1", IP: "10.81.0.4"})
	})
	_ = a.Step(context.Background())
	if sys.count("restart")+sys.count("reload") != 0 {
		t.Fatalf("a new device restarted ocserv: %v", sys.calls)
	}
	if _, ok := sys.lookup("s1-tv"); !ok {
		t.Fatal("RADIUS does not know the new device")
	}
	// A renewed certificate: reloaded.
	cert, key, _ := occonf.NewServerCert([]string{"vpn.example.com"})
	files.state = ocState(t, func(s *occonf.State) { s.Interface.Cert, s.Interface.Key = cert, key })
	_ = a.Step(context.Background())
	if sys.count("reload") != 1 || sys.count("restart") != 0 {
		t.Fatalf("a new certificate: %v", sys.calls)
	}
	// A new port: restarted.
	files.state = ocState(t, func(s *occonf.State) {
		s.Interface.Cert, s.Interface.Key = cert, key
		s.Interface.Port = 8443
	})
	_ = a.Step(context.Background())
	if sys.count("restart") != 1 {
		t.Fatalf("a new port: %v", sys.calls)
	}
	// ocserv died: started again.
	sys.running = false
	_ = a.Step(context.Background())
	if sys.count("start") != 2 {
		t.Fatalf("ocserv down: %v", sys.calls)
	}
}

// An agent that restarts -- an update -- finds the secret it wrote, so the
// files are unchanged and nobody is disconnected.
func TestAnAgentRestartDisconnectsNobody(t *testing.T) {
	a, sys, files := ocRig(t)
	_ = a.Step(context.Background())
	secret := sys.secret
	sys.users = []OCUser{{ID: 7, Username: "s1-laptop"}}
	b := NewOpenConnect(sys, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.now = a.now
	_ = b.Step(context.Background())
	if sys.secret != secret {
		t.Fatal("a restarted agent drew a new RADIUS secret")
	}
	if sys.count("restart")+sys.count("reload") != 0 || sys.count("start") != 1 || len(sys.ended) != 0 {
		t.Fatalf("a restarted agent touched ocserv or its sessions: %v ended %v", sys.calls, sys.ended)
	}
}

// RADIUS ends a sign-in's other sessions before it lets the new one in.
func TestSigningInAgainEndsTheOtherSession(t *testing.T) {
	a, sys, _ := ocRig(t)
	_ = a.Step(context.Background())
	sys.users = []OCUser{{ID: 7, Username: "s1-laptop"}, {ID: 8, Username: "s1-phone"}}
	sys.onAccept("s1-laptop")
	if len(sys.ended) != 1 || sys.ended[0] != 7 {
		t.Fatalf("ended %v, want the laptop's old session", sys.ended)
	}
	u, ok := sys.lookup("s1-laptop")
	if !ok || u.Password != "Laptop123" || u.IP.String() != "10.81.0.2" || len(u.DNS) != 1 {
		t.Fatalf("RADIUS knows %+v %v", u, ok)
	}
	if _, ok := sys.lookup("stranger"); ok {
		t.Fatal("RADIUS knows a stranger")
	}
}

// The pass ends what should not be: a device taken off, one kicked, one
// whose password changed, and the older of two on one sign-in.
func TestOpenConnectSessionsThatShouldNotExistAreEnded(t *testing.T) {
	a, sys, files := ocRig(t)
	sys.users = []OCUser{{ID: 10, Username: "s1-laptop", Device: "vpns0", Remote: "198.51.100.7", RX: 5, TX: 6}, {ID: 11, Username: "s1-phone", Device: "vpns1"}}
	_ = a.Step(context.Background())
	if len(sys.ended) != 0 {
		t.Fatalf("ended %v", sys.ended)
	}
	if got := files.report.Sessions; len(got) != 2 || got[0].Account != 1 || got[0].Device != "vpns0" || got[0].Endpoint != "198.51.100.7" || got[0].RX != 5 {
		t.Fatalf("report %+v", got)
	}
	// The phone again, from elsewhere, seen later: the older goes.
	a.now = func() time.Time { return time.Unix(1_000_010, 0) }
	sys.users = append(sys.users, OCUser{ID: 12, Username: "s1-phone", Device: "vpns2"})
	_ = a.Step(context.Background())
	if len(sys.ended) != 1 || sys.ended[0] != 11 {
		t.Fatalf("ended %v, want the older phone session", sys.ended)
	}
	// The laptop's password changes.
	files.state = ocState(t, func(s *occonf.State) { s.Interface.Accounts[0].Secret = "Rotated999" })
	_ = a.Step(context.Background())
	if sys.ended[len(sys.ended)-1] != 10 {
		t.Fatalf("ended %v, want the laptop after its password changed", sys.ended)
	}
	// Kicked, then taken off.
	files.state = ocState(t, nil)
	files.kicks = []uint{2}
	_ = a.Step(context.Background())
	if sys.ended[len(sys.ended)-1] != 12 {
		t.Fatalf("ended %v, want the phone on request", sys.ended)
	}
	sys.users = append(sys.users, OCUser{ID: 13, Username: "s1-phone"})
	files.state = ocState(t, func(s *occonf.State) { s.Interface.Accounts = s.Interface.Accounts[:1] })
	_ = a.Step(context.Background())
	if sys.ended[len(sys.ended)-1] != 13 {
		t.Fatalf("ended %v, want the phone taken off", sys.ended)
	}
}

func TestCamouflageAnOcservCannotDoIsSaid(t *testing.T) {
	a, sys, files := ocRig(t)
	files.state = ocState(t, func(s *occonf.State) {
		s.Interface.Camouflage, s.Interface.CamouflageSecret = true, "Hidden42Door"
	})
	_ = a.Step(context.Background())
	if files.report.Notice == "" || files.report.State != l2tpconf.AgentRunning {
		t.Fatalf("report %+v", files.report)
	}
	sys.camouflage = true
	_ = a.Step(context.Background())
	if files.report.Notice != "" {
		t.Fatalf("notice on an ocserv that can: %q", files.report.Notice)
	}
}

func TestNoOpenConnectStateTakesItAway(t *testing.T) {
	a, sys, files := ocRig(t)
	_ = a.Step(context.Background())
	files.state = nil
	if err := a.Step(context.Background()); !errors.Is(err, ErrNoTunnel) {
		t.Fatalf("err %v", err)
	}
	if sys.count("remove") != 1 || sys.count("radius-stop") != 1 {
		t.Fatalf("not taken away: %v", sys.calls)
	}
	_ = a.Step(context.Background())
	if sys.count("remove") != 1 {
		t.Fatal("taken away twice")
	}
}

func TestARefusedOpenConnectStateChangesNothing(t *testing.T) {
	a, sys, files := ocRig(t)
	files.state = []byte(`{"version":1,"interface":{"id":1,"name":"bad name"}}`)
	if err := a.Step(context.Background()); err == nil {
		t.Fatal("accepted")
	}
	if len(sys.files) != 0 || sys.count("start") != 0 || files.report.State != l2tpconf.AgentFailed {
		t.Fatalf("calls %v report %+v", sys.calls, files.report)
	}
}
