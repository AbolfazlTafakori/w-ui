package reconciler

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// newReconciler builds one with no kernel and no tunnels: these tests are about
// the bridge from a node's report into the shared allowance, and nothing below
// that is involved.
func newReconciler(t *testing.T, db *gorm.DB) *Reconciler {
	t.Helper()
	return New(Options{
		DB:       db,
		Enforcer: newFakeEnforcer(),
		Pool:     backend.NewPool(quietLog()),
		Interval: time.Second,
		Log:      quietLog(),
	})
}

// One allowance, spent wherever the customer connects.
//
// This is the bridge: bytes a customer spent on a node arrive here and have to
// land in the same UsedBytes the local kernel feeds. If they do not, a customer
// sold three servers gets three separate allowances and pays for one — and it
// fails quietly, because the central panel's own numbers stay perfectly
// consistent with themselves.
func TestNodeUsageIsSpentFromTheSameAllowance(t *testing.T) {
	db := newTestDB(t)

	c := model.Client{
		Name: "Roya", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
		QuotaBytes: 10 << 30, UsedBytes: 1 << 30,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}

	r := newReconciler(t, db)
	r.AddNodeUsage(c.ID, 2<<30, 512<<20, 1536<<20)
	flush(t, r)

	var got model.Client
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.UsedBytes != 3<<30 {
		t.Errorf("after spending 2 GB on a node the customer has used %d, want %d",
			got.UsedBytes, uint64(3)<<30)
	}
	if got.UpBytes != 512<<20 || got.DownBytes != 1536<<20 {
		t.Errorf("the directions were lost: up=%d down=%d", got.UpBytes, got.DownBytes)
	}
}

// Spending on a node is what tips a customer over, the same as spending here.
// Otherwise a customer could use their whole allowance on nodes and never be
// cut off anywhere.
func TestSpendingOnANodeCanExhaustACustomer(t *testing.T) {
	db := newTestDB(t)

	c := model.Client{
		Name: "Roya", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
		QuotaBytes: 1 << 30, UsedBytes: 0,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}

	r := newReconciler(t, db)
	r.AddNodeUsage(c.ID, 1<<30, 0, 1<<30)
	flush(t, r)
	r.Tick(context.Background())

	var got model.Client
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusExhausted {
		t.Errorf("a customer who spent their whole allowance on a node is %q, want exhausted",
			got.Status)
	}
}

// An idle node reports zero, every interval, for every customer on it. Writing
// those would be a row per customer per twenty seconds for nothing.
func TestNothingIsWrittenForANodeThatCarriedNothing(t *testing.T) {
	db := newTestDB(t)

	c := model.Client{
		Name: "Roya", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
		QuotaBytes: 10 << 30,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}

	r := newReconciler(t, db)
	r.AddNodeUsage(c.ID, 0, 0, 0)
	flush(t, r)

	var samples int64
	if err := db.Model(&model.TrafficSample{}).Count(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if samples != 0 {
		t.Errorf("an idle node wrote %d traffic rows", samples)
	}
}

// flush waits for the writer to have applied what was submitted.
//
// Usage is batched rather than written per report — a hundred customers across
// ten nodes would otherwise be a thousand small writes a minute — so a test has
// to wait for the batch rather than read straight after submitting.
func flush(t *testing.T, r *Reconciler) {
	t.Helper()
	r.writer.flush(context.Background())
	time.Sleep(10 * time.Millisecond)
}
