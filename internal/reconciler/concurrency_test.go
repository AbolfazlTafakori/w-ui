package reconciler

import (
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
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
