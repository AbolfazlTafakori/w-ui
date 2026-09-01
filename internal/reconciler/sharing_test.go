package reconciler

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestPortIsStrippedFromAnEndpoint(t *testing.T) {
	// A customer's source port changes on every reconnect. Keeping it would
	// turn one ordinary customer into a hundred apparent sharers.
	cases := map[string]string{
		"203.0.113.7:51820":  "203.0.113.7",
		"203.0.113.7":        "203.0.113.7",
		"[2001:db8::1]:4444": "2001:db8::1",
		"  198.51.100.4:80 ": "198.51.100.4",
		"":                   "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func seedSharing(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()

	client := model.Client{Name: "Ali", Status: model.StatusActive, DeviceLimit: 1}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("client: %v", err)
	}
	acc := model.Account{
		ClientID: client.ID, InterfaceID: 1, NodeID: 1,
		DeviceName: "Laptop", IP: "10.66.0.2", Enabled: true,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("account: %v", err)
	}

	quiet := model.Client{Name: "Sara", Status: model.StatusActive, DeviceLimit: 1}
	if err := db.Create(&quiet).Error; err != nil {
		t.Fatalf("client: %v", err)
	}
	quietAcc := model.Account{
		ClientID: quiet.ID, InterfaceID: 1, NodeID: 1,
		DeviceName: "PC", IP: "10.66.0.3", Enabled: true,
	}
	if err := db.Create(&quietAcc).Error; err != nil {
		t.Fatalf("account: %v", err)
	}
}

func TestOneAddressIsNotSharing(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)

	r := &Reconciler{db: db, log: quietLog()}
	if err := recordEndpoints(context.Background(), db, map[uint]string{1: "203.0.113.7:1"}, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := r.Sharing(context.Background())
	if err != nil {
		t.Fatalf("sharing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a single address was reported as sharing: %+v", got)
	}
}

func TestTwoAddressesAreNotYetSharing(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()

	// A phone moving between wifi and mobile data reaches two addresses on its
	// own. Reporting that would cry wolf on ordinary customers.
	for _, addr := range []string{"203.0.113.7:1", "198.51.100.4:2"} {
		if err := recordEndpoints(ctx, db, map[uint]string{1: addr}, now); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	r := &Reconciler{db: db, log: quietLog()}
	got, err := r.Sharing(ctx)
	if err != nil {
		t.Fatalf("sharing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("two addresses were reported as sharing: %+v", got)
	}
}

func TestThreeLiveAddressesAreReported(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()

	for _, addr := range []string{"203.0.113.7:1", "198.51.100.4:2", "192.0.2.9:3"} {
		if err := recordEndpoints(ctx, db, map[uint]string{1: addr}, now); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	r := &Reconciler{db: db, log: quietLog()}
	got, err := r.Sharing(ctx)
	if err != nil {
		t.Fatalf("sharing: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d reports, want 1: %+v", len(got), got)
	}
	if got[0].ClientName != "Ali" || got[0].DeviceName != "Laptop" {
		t.Errorf("report names the wrong account: %+v", got[0])
	}
	// The addresses themselves are the point: an operator has to be able to
	// tell a real case from a customer behind a carrier NAT.
	if len(got[0].Addrs) != 3 {
		t.Errorf("addrs = %v, want all three", got[0].Addrs)
	}
}

func TestAddressesOutsideTheWindowDoNotCount(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()

	// Two from this morning and one from now is one customer over a day, not
	// three at once.
	old := now.Add(-2 * sharingWindow)
	for _, addr := range []string{"203.0.113.7:1", "198.51.100.4:2"} {
		if err := recordEndpoints(ctx, db, map[uint]string{1: addr}, old); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := recordEndpoints(ctx, db, map[uint]string{1: "192.0.2.9:3"}, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	r := &Reconciler{db: db, log: quietLog()}
	got, err := r.Sharing(ctx)
	if err != nil {
		t.Fatalf("sharing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("stale addresses were counted as live: %+v", got)
	}
}

func TestReconnectingKeepsTheOriginalFirstSeen(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()

	first := now.Add(-time.Hour)
	if err := recordEndpoints(ctx, db, map[uint]string{1: "203.0.113.7:1"}, first); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := recordEndpoints(ctx, db, map[uint]string{1: "203.0.113.7:2"}, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	var row model.AccountEndpoint
	if err := db.Where("account_id = ? AND addr = ?", 1, "203.0.113.7").First(&row).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	// "Since when" is the useful half of the answer, so an upsert must not
	// overwrite it with the latest sighting.
	if !row.FirstSeen.Equal(first.Truncate(time.Second)) && row.FirstSeen.After(first.Add(time.Second)) {
		t.Errorf("firstSeen = %v, want it to stay at %v", row.FirstSeen, first)
	}
	if row.Hits != 2 {
		t.Errorf("hits = %d, want 2", row.Hits)
	}
	if !row.LastSeen.After(first) {
		t.Errorf("lastSeen = %v, want it moved forward", row.LastSeen)
	}
}

func TestPruneDropsOnlyLongDeadAddresses(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()

	if err := recordEndpoints(ctx, db, map[uint]string{1: "203.0.113.7:1"},
		now.Add(-endpointRetention-time.Hour)); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := recordEndpoints(ctx, db, map[uint]string{2: "198.51.100.4:1"}, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := pruneEndpoints(ctx, db, now); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var left []model.AccountEndpoint
	if err := db.Find(&left).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(left) != 1 || left[0].AccountID != 2 {
		t.Errorf("prune kept %+v, want only the recent address", left)
	}
}

func TestAnEmptyEndpointIsIgnored(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)

	// A peer that has never handshaken reports no endpoint. Storing a blank
	// would create a phantom address shared by every idle account.
	if err := recordEndpoints(context.Background(), db, map[uint]string{1: ""}, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	var n int64
	db.Model(&model.AccountEndpoint{}).Count(&n)
	if n != 0 {
		t.Errorf("stored %d rows for a blank endpoint, want 0", n)
	}
}

func TestPlanDoesNotStartUntilTheCustomerConnects(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()

	c := model.Client{
		Name: "Ali", Status: model.StatusActive, DeviceLimit: 1,
		StartOnFirstUse: true, DurationDays: 30,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	acc := model.Account{
		ClientID: c.ID, InterfaceID: 1, NodeID: 1,
		DeviceName: "Laptop", IP: "10.66.0.2", Enabled: true,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}

	r := &Reconciler{db: db, log: quietLog()}

	// Handed out but never used: a reseller's configs must not burn through
	// their month sitting in someone's inbox.
	started, err := r.activate(context.Background(), now)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if started != 0 {
		t.Fatalf("started %d plans before anyone connected", started)
	}

	var check model.Client
	db.First(&check, c.ID)
	if check.ExpiresAt != nil {
		t.Errorf("expiry was set before first use: %v", check.ExpiresAt)
	}

	// Now they connect.
	db.Model(&model.Account{}).Where("id = ?", acc.ID).Update("last_handshake", now)
	started, err = r.activate(context.Background(), now)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if started != 1 {
		t.Fatalf("started %d plans on first use, want 1", started)
	}

	db.First(&check, c.ID)
	if check.ActivatedAt == nil || check.ExpiresAt == nil {
		t.Fatalf("plan did not start: activated=%v expires=%v", check.ActivatedAt, check.ExpiresAt)
	}
	if days := check.ExpiresAt.Sub(now).Hours() / 24; days < 29 || days > 31 {
		t.Errorf("expiry is %.1f days out, want 30", days)
	}
}

func TestAStartedPlanIsNeverRestarted(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()

	started := now.Add(-10 * 24 * time.Hour)
	expires := started.AddDate(0, 0, 30)
	c := model.Client{
		Name: "Ali", Status: model.StatusActive, DeviceLimit: 1,
		StartOnFirstUse: true, DurationDays: 30,
		ActivatedAt: &started, ExpiresAt: &expires,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	acc := model.Account{
		ClientID: c.ID, InterfaceID: 1, NodeID: 1,
		DeviceName: "Laptop", IP: "10.66.0.2", Enabled: true, LastHandshake: &now,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}

	r := &Reconciler{db: db, log: quietLog()}
	// Every later connection must not push the expiry out; that would make the
	// plan renew itself for as long as the customer keeps using it.
	if n, err := r.activate(context.Background(), now); err != nil || n != 0 {
		t.Fatalf("activate = %d, %v; want it to leave a started plan alone", n, err)
	}

	var check model.Client
	db.First(&check, c.ID)
	if !check.ExpiresAt.Equal(expires.Truncate(time.Second)) && check.ExpiresAt.After(expires.Add(time.Second)) {
		t.Errorf("expiry moved from %v to %v", expires, check.ExpiresAt)
	}
}
