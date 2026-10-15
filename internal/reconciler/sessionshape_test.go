package reconciler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/shaper"
)

// sessionDriver is a tunnel whose sessions each have a link of their own.
type sessionDriver struct {
	*backend.Memory
	devs []backend.SessionDevice
}

func (s *sessionDriver) SessionDevices(context.Context) ([]backend.SessionDevice, error) {
	return s.devs, nil
}

type shapeCall struct {
	devices []string
	clients []shaper.Client
}

type recordingShaper struct{ calls []shapeCall }

func (f *recordingShaper) Apply(_ context.Context, devices []string, clients []shaper.Client) error {
	f.calls = append(f.calls, shapeCall{devices: devices, clients: clients})
	return nil
}
func (f *recordingShaper) Health(context.Context) error { return nil }
func (f *recordingShaper) Close() error                 { return nil }

// sessionCalls takes what was laid on session links since the last look.
func (f *recordingShaper) sessionCalls() []shapeCall {
	var out []shapeCall
	for _, c := range f.calls {
		if len(c.devices) == 1 && strings.HasPrefix(c.devices[0], "ppp") {
			out = append(out, c)
		}
	}
	f.calls = nil
	return out
}

func TestEachSessionsLinkCarriesItsCustomersRate(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	iface := &model.Interface{
		ID: 1, NodeID: 1, Name: "l2tp0", Protocol: model.ProtocolWireGuard,
		Enabled: true, ListenPort: 1701, Subnet: "10.66.0.0/16",
		EndpointHost: "vpn.example.com", MTU: 1400,
	}
	if err := db.Create(iface).Error; err != nil {
		t.Fatal(err)
	}
	drv := &sessionDriver{Memory: backend.NewMemory(model.ProtocolWireGuard)}
	if err := drv.Open(ctx, iface); err != nil {
		t.Fatal(err)
	}
	sh := &recordingShaper{}
	r := New(Options{
		DB: db, Enforcer: newFakeEnforcer(), Shaper: sh,
		Pool: testPool(iface, drv), Interval: time.Second, Log: quietLog(),
	})

	limited := seed(t, db, model.Client{Name: "Ali", RateBitsPerSec: 1_000_000}, "10.66.0.2")
	seed(t, db, model.Client{Name: "Sara"}, "10.66.0.3")
	var accs []model.Account
	db.Order("id").Find(&accs)
	aliAcc, saraAcc := accs[0].ID, accs[1].ID
	aliKey := enforce.Key(limited)

	drv.devs = []backend.SessionDevice{
		{AccountID: aliAcc, Device: "ppp0", Since: 100},
		{AccountID: saraAcc, Device: "ppp1", Since: 100},
	}
	r.Tick(ctx)
	got := sh.sessionCalls()
	if len(got) != 1 || got[0].devices[0] != "ppp0" || len(got[0].clients) != 1 ||
		got[0].clients[0].Key != aliKey || got[0].clients[0].RateBitsPerSec != 1_000_000 {
		t.Fatalf("first tick laid %+v; want Ali's class on ppp0 and nothing on the unlimited ppp1", got)
	}

	// Nothing changed: the kernel is not asked again.
	r.Tick(ctx)
	if got := sh.sessionCalls(); len(got) != 0 {
		t.Fatalf("an unchanged session was laid again: %+v", got)
	}

	// The same name for a new session is a new device.
	drv.devs[0].Since = 200
	r.Tick(ctx)
	if got := sh.sessionCalls(); len(got) != 1 || got[0].devices[0] != "ppp0" {
		t.Fatalf("a new session on a reused name: %+v", got)
	}

	// A new rate reaches the live link.
	db.Model(&model.Client{}).Where("id = ?", limited).Update("rate_bits_per_sec", 2_000_000)
	r.Tick(ctx)
	if got := sh.sessionCalls(); len(got) != 1 || got[0].clients[0].RateBitsPerSec != 2_000_000 {
		t.Fatalf("a changed rate: %+v", got)
	}

	// A rate taken away is taken off the link.
	db.Model(&model.Client{}).Where("id = ?", limited).Update("rate_bits_per_sec", 0)
	r.Tick(ctx)
	if got := sh.sessionCalls(); len(got) != 1 || len(got[0].clients) != 0 {
		t.Fatalf("a removed rate: %+v", got)
	}
	r.Tick(ctx)
	if got := sh.sessionCalls(); len(got) != 0 {
		t.Fatalf("an unlimited link was touched again: %+v", got)
	}

	// A session that ends is forgotten.
	drv.devs = drv.devs[1:]
	r.Tick(ctx)
	r.sessions.mu.Lock()
	_, kept := r.sessions.by["ppp0"]
	r.sessions.mu.Unlock()
	if kept {
		t.Fatal("an ended session's link is still remembered")
	}
}

// A tunnel's own device is shaped where its customers' traffic leaves by it:
// WireGuard's, and IKEv2's xfrm device; never L2TP's, PPTP's or
// OpenConnect's placeholders, whose sessions have devices of their own.
func TestTheShapedDevicesAreTheTunnelsThatCarryTraffic(t *testing.T) {
	got := shapedDevices([]model.Interface{
		{Name: "wg0", Protocol: model.ProtocolWireGuard},
		{Name: "ike0", Protocol: model.ProtocolIKEv2},
		{Name: "l2tp0", Protocol: model.ProtocolL2TP},
		{Name: "pptp0", Protocol: model.ProtocolPPTP},
		{Name: "oc0", Protocol: model.ProtocolOpenConnect},
	})
	if strings.Join(got, ",") != "ike0,wg0" {
		t.Fatalf("shaped %v", got)
	}
}
