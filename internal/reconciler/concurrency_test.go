package reconciler

import (
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

func stat(id uint, bytes uint64, ep string) backend.Stat {
	return backend.Stat{AccountID: id, RX: bytes, Endpoint: ep + ":51820"}
}

// One plan, one connection at a time: the phone that came second waits
// while the laptop that was already on stays.
func TestSecondDeviceIsHeldOffWhileTheFirstIsConnected(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{{ID: 10, DeviceName: "laptop"}, {ID: 11, DeviceName: "phone"}}

	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1"), stat(11, 100, "2.2.2.2")}, t0)
	if got := c.enforce(client, accs, t0); got != nil {
		t.Fatalf("first sight held something off: %+v", got)
	}
	// The laptop moves bytes; the phone is idle.
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1"), stat(11, 100, "2.2.2.2")}, t0.Add(2*time.Second))
	if got := c.enforce(client, accs, t0.Add(2*time.Second)); got != nil {
		t.Fatalf("one device held off: %+v", got)
	}
	// Now the phone connects too.
	c.observe([]backend.Stat{stat(10, 300, "1.1.1.1"), stat(11, 200, "2.2.2.2")}, t0.Add(4*time.Second))
	got := c.enforce(client, accs, t0.Add(4*time.Second))
	if len(got) != 1 || got[0].Account.ID != 11 {
		t.Fatalf("expected the phone held off, got %+v", got)
	}
	if !c.heldOff(11, t0.Add(5*time.Second)) || c.heldOff(10, t0.Add(5*time.Second)) {
		t.Fatal("the wrong device is held off")
	}
	// While held it is not counted again.
	if got := c.enforce(client, accs, t0.Add(6*time.Second)); got != nil {
		t.Fatalf("held twice: %+v", got)
	}
	// The hold ends on its own.
	if c.heldOff(11, t0.Add(holdFor+10*time.Second)) {
		t.Fatal("hold did not end")
	}
}

// Switching devices is what the files are for: once the laptop goes quiet
// the phone connects without being held.
func TestSwitchingDevicesIsAllowed(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{{ID: 10}, {ID: 11}}
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1"), stat(11, 100, "2.2.2.2")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1"), stat(11, 100, "2.2.2.2")}, t0.Add(2*time.Second))
	later := t0.Add(activeWindow + 5*time.Second)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1"), stat(11, 200, "2.2.2.2")}, later)
	if got := c.enforce(client, accs, later); got != nil {
		t.Fatalf("a switch was held off: %+v", got)
	}
}

// A phone walking from wifi onto mobile data changes address once; that is
// not two people. Two devices on one file keep flipping, and that is.
func TestOneFileOnTwoDevicesAtOnceIsHeldButANetworkChangeIsNot(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{{ID: 10}}
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, t0.Add(2*time.Second))
	c.observe([]backend.Stat{stat(10, 300, "2.2.2.2")}, t0.Add(4*time.Second)) // wifi -> LTE
	c.observe([]backend.Stat{stat(10, 400, "2.2.2.2")}, t0.Add(6*time.Second))
	if got := c.enforce(client, accs, t0.Add(6*time.Second)); got != nil {
		t.Fatalf("a network change was held off: %+v", got)
	}
	c.observe([]backend.Stat{stat(10, 500, "1.1.1.1")}, t0.Add(8*time.Second)) // and back: two devices
	got := c.enforce(client, accs, t0.Add(8*time.Second))
	if len(got) != 1 {
		t.Fatalf("a file fought over by two devices was not held off: %+v", got)
	}
}

// A plan for two lets two connect and holds the third.
func TestLimitOfTwoAllowsTwo(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 2}
	accs := []model.Account{{ID: 10}, {ID: 11}, {ID: 12}}
	all := func(b uint64) []backend.Stat {
		return []backend.Stat{stat(10, b, "1.1.1.1"), stat(11, b, "2.2.2.2"), stat(12, b, "3.3.3.3")}
	}
	c.observe(all(100), t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1"), stat(11, 200, "2.2.2.2"), stat(12, 100, "3.3.3.3")}, t0.Add(2*time.Second))
	if got := c.enforce(client, accs, t0.Add(2*time.Second)); got != nil {
		t.Fatalf("two on a plan for two held off: %+v", got)
	}
	c.observe(all(300), t0.Add(4*time.Second))
	got := c.enforce(client, accs, t0.Add(4*time.Second))
	if len(got) != 1 || got[0].Account.ID != 12 {
		t.Fatalf("expected the third held off, got %+v", got)
	}
}

// The limit is the customer's, not a tunnel's: WireGuard on the phone and
// OpenVPN on the laptop are two connections of one plan.
func TestLimitSpansProtocols(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{
		{ID: 10, InterfaceID: 1, DeviceName: "phone-wg"},
		{ID: 11, InterfaceID: 2, DeviceName: "laptop-ovpn", Username: "roya"},
	}
	// Two drivers report separately, as they do in a tick.
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, t0)
	c.observe([]backend.Stat{stat(11, 100, "2.2.2.2")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, t0.Add(2*time.Second))
	c.observe([]backend.Stat{stat(11, 100, "2.2.2.2")}, t0.Add(2*time.Second))
	if got := c.enforce(client, accs, t0.Add(2*time.Second)); got != nil {
		t.Fatalf("the phone alone was held off: %+v", got)
	}
	c.observe([]backend.Stat{stat(10, 300, "1.1.1.1")}, t0.Add(4*time.Second))
	c.observe([]backend.Stat{stat(11, 200, "2.2.2.2")}, t0.Add(4*time.Second))
	got := c.enforce(client, accs, t0.Add(4*time.Second))
	if len(got) != 1 || got[0].Account.ID != 11 {
		t.Fatalf("expected the OpenVPN laptop held off while the WireGuard phone stays, got %+v", got)
	}
}

// The limit spans servers: a device live on a node is one of the customer's
// connections, and when it is the newer one it is the one held -- there.
func TestLimitSpansNodes(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{
		{ID: 10, ClientID: 1, NodeID: 1, InterfaceID: 1, DeviceName: "phone-here"},
		{ID: 11, ClientID: 1, NodeID: 5, InterfaceID: 9, DeviceName: "laptop-on-node"},
	}
	c.remember(accs, nil, 1, t0)

	// The phone is live on this server.
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, t0.Add(2*time.Second))
	// The node reports the laptop just came on.
	c.setRemote(5, []service.NodeSession{{OriginID: 11, Connections: 1, AgeSeconds: 0}}, t0.Add(3*time.Second))

	got := c.enforce(client, accs, t0.Add(3*time.Second))
	if len(got) != 1 || got[0].Account.ID != 11 || got[0].Node != 5 {
		t.Fatalf("expected the laptop held off on node 5, got %+v", got)
	}
	// Its hold is what the syncer carries with the next push.
	if _, ok := c.holds(t0.Add(4 * time.Second))[11]; !ok {
		t.Fatal("the hold is not listed for the push")
	}
	// The list agrees: one connection, the held one not counted.
	if n := c.connectionsNow([]uint{1}, t0.Add(4*time.Second))[1]; n != 1 {
		t.Fatalf("connections now = %d, want 1", n)
	}
}

// The other way round: the laptop on the node was there first, so the
// phone arriving here is the one held, on this server.
func TestTheOlderConnectionOnANodeIsKept(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{
		{ID: 10, NodeID: 1, InterfaceID: 1},
		{ID: 11, NodeID: 5, InterfaceID: 9},
	}
	c.remember(accs, nil, 1, t0)
	c.setRemote(5, []service.NodeSession{{OriginID: 11, Connections: 1, AgeSeconds: 60}}, t0)
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, t0.Add(2*time.Second))
	got := c.enforce(client, accs, t0.Add(2*time.Second))
	if len(got) != 1 || got[0].Account.ID != 10 || got[0].Node != 0 {
		t.Fatalf("expected the phone held off here, got %+v", got)
	}
}

// A node that has gone quiet is not believed for long: its last report
// stops counting, rather than holding a customer to a stale picture.
func TestAStaleNodeReportIsNotCounted(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{{ID: 10, NodeID: 1}, {ID: 11, NodeID: 5}}
	c.remember(accs, nil, 1, t0)
	c.setRemote(5, []service.NodeSession{{OriginID: 11, Connections: 1}}, t0)
	later := t0.Add(remoteTTL + 5*time.Second)
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, later)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, later.Add(2*time.Second))
	if got := c.enforce(client, accs, later.Add(2*time.Second)); got != nil {
		t.Fatalf("a stale report held a device off: %+v", got)
	}
}

// What a node reports upward: its managed credentials that are live, by
// their id on the panel, with how long they have been on.
func TestANodeReportsItsLiveManagedSessions(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	c.remember([]model.Account{
		{ID: 1, NodeID: 1, OriginID: 40},
		{ID: 2, NodeID: 1, OriginID: 41},
		{ID: 3, NodeID: 1, OriginID: 0}, // this server's own customer: not the panel's business
	}, nil, 1, t0)
	all := func(b uint64) []backend.Stat {
		return []backend.Stat{stat(1, b, "1.1.1.1"), stat(2, b, "2.2.2.2"), stat(3, b, "3.3.3.3")}
	}
	c.observe(all(100), t0)
	c.observe([]backend.Stat{stat(1, 200, "1.1.1.1"), stat(2, 100, "2.2.2.2"), stat(3, 200, "3.3.3.3")}, t0.Add(2*time.Second))
	got := c.sessions(t0.Add(12 * time.Second))
	if len(got) != 1 || got[0].OriginID != 40 || got[0].Connections != 1 || got[0].AgeSeconds != 10 {
		t.Fatalf("sessions = %+v", got)
	}
	if len(got[0].Addrs) != 1 || got[0].Addrs[0] != "1.1.1.1:51820" {
		t.Fatalf("addrs = %v", got[0].Addrs)
	}
}

// A hold the panel decided lands on the node like one of its own.
func TestAHoldFromThePanelIsHonoured(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	c.hold(7, t0.Add(holdFor))
	if !c.heldOff(7, t0.Add(time.Second)) {
		t.Fatal("not held")
	}
	if c.heldOff(7, t0.Add(holdFor+time.Second)) {
		t.Fatal("still held after it ended")
	}
	// A shorter hold does not cut a longer one short.
	c.hold(8, t0.Add(holdFor))
	c.hold(8, t0.Add(10*time.Second))
	if !c.heldOff(8, t0.Add(30*time.Second)) {
		t.Fatal("a later, shorter hold cut the first one short")
	}
}

// The fallback: a node decides for itself only once its panel has been
// quiet for a while.
func TestANodeFallsBackOnlyWhenThePanelIsQuiet(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	if !c.panelSilent(t0) {
		t.Fatal("never heard from a panel should count as silent")
	}
	c.panelSpoke(t0)
	if c.panelSilent(t0.Add(10 * time.Second)) {
		t.Fatal("silent ten seconds after the panel spoke")
	}
	if !c.panelSilent(t0.Add(panelSilence + time.Second)) {
		t.Fatal("not silent after the panel went quiet")
	}
}

// A device coming on and going quiet is said once each, by name and address.
func TestConnectAndDisconnectAreNoticedOnce(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	accs := []model.Account{{ID: 10, ClientID: 1, NodeID: 1, DeviceName: "phone"}}
	names := map[uint]string{1: "Roya"}
	c.remember(accs, names, 1, t0)
	c.observe([]backend.Stat{stat(10, 100, "1.1.1.1")}, t0)
	c.observe([]backend.Stat{stat(10, 200, "1.1.1.1")}, t0.Add(2*time.Second))
	ev := c.Events()
	if len(ev) != 1 || ev[0].Kind != "connected" || ev[0].Name != "Roya / phone" || ev[0].Addr != "1.1.1.1:51820" {
		t.Fatalf("events after connecting: %+v", ev)
	}
	c.observe([]backend.Stat{stat(10, 300, "1.1.1.1")}, t0.Add(4*time.Second))
	if ev := c.Events(); len(ev) != 0 {
		t.Fatalf("a live device was announced again: %+v", ev)
	}
	later := t0.Add(4*time.Second + activeWindow)
	c.remember(accs, names, 1, later)
	ev = c.Events()
	if len(ev) != 1 || ev[0].Kind != "disconnected" || ev[0].For < 2*time.Second {
		t.Fatalf("events after going quiet: %+v", ev)
	}
	c.remember(accs, names, 1, later.Add(time.Minute))
	if ev := c.Events(); len(ev) != 0 {
		t.Fatalf("a quiet device was announced again: %+v", ev)
	}
}

// Behind a relay every device arrives from one address and only the port
// differs; a file on two devices still shows as the fight it is.
func TestTwoDevicesBehindARelayAreToldApartByPort(t *testing.T) {
	c := newConcurrency()
	t0 := time.Now()
	client := &model.Client{ID: 1, DeviceLimit: 1}
	accs := []model.Account{{ID: 10, ClientID: 1, NodeID: 1}}
	c.remember(accs, nil, 1, t0)
	ep := func(port string, b uint64) []backend.Stat {
		return []backend.Stat{{AccountID: 10, RX: b, Endpoint: "127.0.0.1:" + port}}
	}
	c.observe(ep("7273", 100), t0)
	c.observe(ep("7273", 200), t0.Add(2*time.Second))
	c.observe(ep("55854", 300), t0.Add(4*time.Second))
	c.observe(ep("7273", 400), t0.Add(6*time.Second))
	got := c.enforce(client, accs, t0.Add(6*time.Second))
	if len(got) != 1 {
		t.Fatalf("two devices on one file behind a relay were not caught: %+v", got)
	}
}
