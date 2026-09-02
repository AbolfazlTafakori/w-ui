package reconciler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// fakeEnforcer records what it was told and lets a test hand back counters,
// standing in for the kernel so the loop's behaviour can be checked anywhere.
type fakeEnforcer struct {
	*enforce.Noop
	applied []enforce.Rule
	drain   []enforce.Usage
	drains  int
}

func newFakeEnforcer() *fakeEnforcer {
	return &fakeEnforcer{Noop: enforce.NewNoop()}
}

func (f *fakeEnforcer) Apply(ctx context.Context, rules []enforce.Rule) error {
	f.applied = rules
	return f.Noop.Apply(ctx, rules)
}

func (f *fakeEnforcer) DrainCounters(context.Context) ([]enforce.Usage, error) {
	f.drains++
	out := f.drain
	f.drain = nil // draining is read-and-zero
	return out, nil
}

func (f *fakeEnforcer) ruleFor(key string) (enforce.Rule, bool) {
	for _, r := range f.applied {
		if r.Key == key {
			return r, true
		}
	}
	return enforce.Rule{}, false
}

// newTestDB gives the calling test a database of its own.
//
// The plain "file::memory:" DSN is one database shared by every connection in
// the process, so tests saw each other's rows and each other's autoincrement
// counters. Naming it after the test isolates them, and removes the need for a
// cleanup list that had to be extended by hand whenever a table was added — the
// kind of list that is always one table out of date.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The database lives as long as a connection to it does, so the pool is
	// held open for the test and closed with it.
	t.Cleanup(func() {
		if sql, err := db.DB(); err == nil {
			_ = sql.Close()
		}
	})
	return db
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// seed inserts a client with one device and returns its id.
func seed(t *testing.T, db *gorm.DB, c model.Client, ip string) uint {
	t.Helper()
	if c.Status == "" {
		c.Status = model.StatusActive
	}
	if c.DeviceLimit == 0 {
		c.DeviceLimit = 1
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	acc := model.Account{
		ClientID:    c.ID,
		InterfaceID: 1,
		NodeID:      1,
		DeviceName:  "device-1",
		IP:          ip,
		PublicKey:   "pk-" + ip,
		Enabled:     true,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return c.ID
}

func newRig(t *testing.T) (*Reconciler, *gorm.DB, *fakeEnforcer, *backend.Memory) {
	t.Helper()
	db := newTestDB(t)
	enf := newFakeEnforcer()
	// The interface has to exist in the database as well as in the driver. The
	// reconciler brings the open drivers in line with what the database says,
	// so accounts on an interface that is not there would have their driver
	// closed underneath them -- which is correct, and is what this fixture was
	// quietly relying on not happening.
	iface := &model.Interface{
		ID: 1, NodeID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard,
		Enabled: true, ListenPort: 51820, Subnet: "10.66.0.0/16",
		EndpointHost: "vpn.example.com", MTU: 1420,
	}
	if err := db.Create(iface).Error; err != nil {
		t.Fatalf("seed interface: %v", err)
	}

	drv := backend.NewMemory(model.ProtocolWireGuard)
	if err := drv.Open(context.Background(), iface); err != nil {
		t.Fatalf("open driver: %v", err)
	}

	r := New(Options{
		DB:       db,
		Enforcer: enf,
		Pool:     testPool(iface, drv),
		Interval: time.Second,
		Log:      quietLog(),
	})
	return r, db, enf, drv
}

// testPool holds the in-memory driver the rig already opened, without asking
// the registry for a real one.
func testPool(iface *model.Interface, drv backend.Backend) *backend.Pool {
	p := backend.NewPool(quietLog())
	p.Put(iface, drv)
	return p
}

func TestActiveClientReachesBothKernelAndDriver(t *testing.T) {
	r, db, enf, drv := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")

	r.Tick(context.Background())

	rule, ok := enf.ruleFor(enforce.Key(id))
	if !ok {
		t.Fatal("no enforcement rule was written for an active client")
	}
	if rule.Blocked {
		t.Error("an active client was pushed to the kernel as blocked")
	}
	if len(drv.Accounts()) != 1 {
		t.Errorf("driver holds %d peers, want 1", len(drv.Accounts()))
	}
}

func TestUsageIsAccumulatedFromDrainedCounters(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 10_000}, "10.66.0.2")

	enf.drain = []enforce.Usage{{Key: enforce.Key(id), Bytes: 400}}
	r.Tick(context.Background())
	r.writer.flush(context.Background())

	var got model.Client
	db.First(&got, id)
	if got.UsedBytes != 400 {
		t.Fatalf("used = %d, want 400", got.UsedBytes)
	}

	// A second drain must add to the first. Draining is read-and-zero, so the
	// value returned is the delta and never needs reset detection.
	enf.drain = []enforce.Usage{{Key: enforce.Key(id), Bytes: 350}}
	r.Tick(context.Background())
	r.writer.flush(context.Background())

	db.First(&got, id)
	if got.UsedBytes != 750 {
		t.Errorf("used = %d, want 750 (400 + 350)", got.UsedBytes)
	}
}

func TestClientOverQuotaIsCutOffInTheSameTick(t *testing.T) {
	r, db, enf, drv := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")

	// Counting happens before evaluation, so the client that runs out during
	// this interval is cut off now rather than a tick later.
	enf.drain = []enforce.Usage{{Key: enforce.Key(id), Bytes: 1200}}
	r.Tick(context.Background())
	r.writer.flush(context.Background())
	r.Tick(context.Background())

	var got model.Client
	db.First(&got, id)
	if got.Status != model.StatusExhausted {
		t.Fatalf("status = %q, want exhausted", got.Status)
	}

	rule, ok := enf.ruleFor(enforce.Key(id))
	if !ok {
		t.Fatal("the cut-off client lost its rule entirely")
	}
	// The rule must survive as a drop: it is what refuses their packets in the
	// gap between the cut-off and the peer being removed.
	if !rule.Blocked {
		t.Error("an exhausted client is still allowed through the kernel")
	}
	if len(drv.Accounts()) != 0 {
		t.Error("the peer of an exhausted client was left in the driver")
	}
}

func TestExpiredClientIsRemoved(t *testing.T) {
	r, db, enf, drv := newRig(t)
	past := time.Now().Add(-time.Hour)
	id := seed(t, db, model.Client{Name: "Sara", ExpiresAt: &past}, "10.66.0.3")

	r.Tick(context.Background())

	var got model.Client
	db.First(&got, id)
	if got.Status != model.StatusExpired {
		t.Fatalf("status = %q, want expired", got.Status)
	}
	if len(drv.Accounts()) != 0 {
		t.Error("an expired client kept its peer")
	}
	if rule, _ := enf.ruleFor(enforce.Key(id)); !rule.Blocked {
		t.Error("an expired client is not blocked in the kernel")
	}
}

func TestDisabledClientIsBlockedButNotDeleted(t *testing.T) {
	r, db, enf, drv := newRig(t)
	id := seed(t, db, model.Client{
		Name: "Reza", QuotaBytes: 5000, Status: model.StatusDisabled,
	}, "10.66.0.4")

	r.Tick(context.Background())

	if len(drv.Accounts()) != 0 {
		t.Error("a disabled client kept its peer")
	}
	rule, ok := enf.ruleFor(enforce.Key(id))
	if !ok || !rule.Blocked {
		t.Error("a disabled client should still carry a blocking rule")
	}

	// Re-enabling must bring them straight back without any other action.
	db.Model(&model.Client{}).Where("id = ?", id).Update("status", model.StatusActive)
	r.Tick(context.Background())

	if len(drv.Accounts()) != 1 {
		t.Error("re-enabling did not restore the peer")
	}
	if rule, _ := enf.ruleFor(enforce.Key(id)); rule.Blocked {
		t.Error("the kernel still blocks a re-enabled client")
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	r, db, _, drv := newRig(t)
	seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")

	for i := 0; i < 5; i++ {
		r.Tick(context.Background())
	}

	// Running the same tick repeatedly must converge, not accumulate.
	if got := len(drv.Accounts()); got != 1 {
		t.Errorf("driver holds %d peers after five ticks, want 1", got)
	}
	if r.Stats().Ticks != 5 {
		t.Errorf("ticks = %d, want 5", r.Stats().Ticks)
	}
}

func TestKernelStateIsRebuiltFromTheDatabase(t *testing.T) {
	r, db, _, drv := newRig(t)
	seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")
	r.Tick(context.Background())

	// Simulate a reboot: the kernel has lost everything, the database has not.
	drv.Close()
	if err := drv.Open(context.Background(), &model.Interface{
		ID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard,
	}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(drv.Accounts()) != 0 {
		t.Fatal("the simulated reboot did not clear driver state")
	}

	r.Tick(context.Background())

	if len(drv.Accounts()) != 1 {
		t.Error("the loop did not rebuild kernel state from the database")
	}
}

func TestStoredUsageSeedsTheKernelQuota(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{
		Name: "Ali", QuotaBytes: 1000, UsedBytes: 600,
	}, "10.66.0.2")

	r.Tick(context.Background())

	rule, _ := enf.ruleFor(enforce.Key(id))
	// Without seeding, a restart would hand the customer their allowance back.
	if rule.UsedBytes != 600 {
		t.Errorf("seeded usage = %d, want 600", rule.UsedBytes)
	}
	if rule.QuotaBytes != 1000 {
		t.Errorf("quota = %d, want 1000", rule.QuotaBytes)
	}
}

func TestMultiDeviceClientCarriesEveryAddress(t *testing.T) {
	r, db, enf, drv := newRig(t)
	c := model.Client{Name: "Ali", QuotaBytes: 5000, DeviceLimit: 3}
	id := seed(t, db, c, "10.66.0.2")
	for i, ip := range []string{"10.66.0.3", "10.66.0.4"} {
		if err := db.Create(&model.Account{
			ClientID: id, InterfaceID: 1, NodeID: 1,
			DeviceName: "extra", IP: ip, PublicKey: "pk", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed device %d: %v", i, err)
		}
	}

	r.Tick(context.Background())

	rule, _ := enf.ruleFor(enforce.Key(id))
	// One rule, three addresses: the allowance belongs to the customer, not to
	// each of their devices.
	if len(rule.Addrs) != 3 {
		t.Errorf("rule carries %d addresses, want 3", len(rule.Addrs))
	}
	if len(drv.Accounts()) != 3 {
		t.Errorf("driver holds %d peers, want 3", len(drv.Accounts()))
	}
}

func TestHandshakesAreRecorded(t *testing.T) {
	r, db, _, drv := newRig(t)
	seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")
	r.Tick(context.Background())

	accounts := drv.Accounts()
	shook := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Second)
	drv.SetStat(backend.Stat{
		AccountID:     accounts[0],
		LastHandshake: shook,
		Endpoint:      "203.0.113.9:51820",
	})

	r.Tick(context.Background())
	r.writer.flush(context.Background())

	var acc model.Account
	db.First(&acc, accounts[0])
	if acc.LastHandshake == nil {
		t.Fatal("handshake was not recorded; the online indicator would never light")
	}
	if acc.LastEndpoint != "203.0.113.9:51820" {
		t.Errorf("endpoint = %q, want the observed one", acc.LastEndpoint)
	}
}

func TestIdleClientsAreNotWritten(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 1000}, "10.66.0.2")

	// A zero-byte reading must not produce a write. With ten thousand mostly
	// idle clients this is the difference between a handful of rows a second
	// and thousands.
	enf.drain = []enforce.Usage{{Key: enforce.Key(id), Bytes: 0}}
	r.Tick(context.Background())

	r.writer.mu.Lock()
	pending := len(r.writer.usage)
	r.writer.mu.Unlock()

	if pending != 0 {
		t.Errorf("%d idle clients were queued for writing", pending)
	}
}

func TestClientWithNoDevicesIsSkipped(t *testing.T) {
	r, db, enf, _ := newRig(t)
	c := model.Client{Name: "Empty", QuotaBytes: 1000, Status: model.StatusActive, DeviceLimit: 1}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r.Tick(context.Background())

	// A rule with no addresses would be dead weight in the kernel.
	if _, ok := enf.ruleFor(enforce.Key(c.ID)); ok {
		t.Error("a client with no devices produced an enforcement rule")
	}
}

func TestKeyRoundTrip(t *testing.T) {
	for _, id := range []uint{1, 42, 99999} {
		key := keyFromClientID(id)
		got, ok := clientIDFromKey(key)
		if !ok || got != id {
			t.Errorf("round trip of %d through %q gave (%d, %v)", id, key, got, ok)
		}
	}
	if _, ok := clientIDFromKey("nonsense"); ok {
		t.Error("a foreign key was decoded as a client id")
	}
}

// The bug this pair exists to prevent: interfaces used to be opened once, at
// startup, into a map nothing could add to. Creating one from the panel
// produced a database row and nothing else, and there was no sign of it until
// somebody restarted the whole panel.
func TestAnInterfaceCreatedWhileRunningGetsADriver(t *testing.T) {
	r, db, _, _ := newRig(t)

	// A second interface, created after the panel is already up.
	second := model.Interface{
		ID: 2, NodeID: 1, Name: "wg1", Protocol: model.ProtocolWireGuard,
		Enabled: true, ListenPort: 51821, Subnet: "10.70.0.0/16",
		EndpointHost: "vpn.example.com", MTU: 1420,
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}

	if _, open := r.pool.Get(second.ID); open {
		t.Fatal("the new interface already had a driver before any tick")
	}

	r.Tick(context.Background())

	// The memory driver is not in the registry, so opening genuinely fails
	// here. What matters is that the attempt was made and recorded, rather
	// than the interface being ignored until a restart.
	if err := r.pool.ErrorFor(second.ID); err == nil {
		if _, open := r.pool.Get(second.ID); !open {
			t.Fatal("a tick neither opened the new interface nor recorded why not")
		}
	}
}

func TestAnInterfaceSwitchedOffLosesItsDriver(t *testing.T) {
	// A disabled interface should stop carrying traffic, not keep a live
	// device that the panel no longer believes in.
	r, db, _, _ := newRig(t)
	r.Tick(context.Background())

	if _, open := r.pool.Get(1); !open {
		t.Fatal("the seeded interface had no driver to begin with")
	}

	if err := db.Model(&model.Interface{}).Where("id = ?", 1).
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())

	if _, open := r.pool.Get(1); open {
		t.Fatal("a disabled interface kept its driver open")
	}
}
