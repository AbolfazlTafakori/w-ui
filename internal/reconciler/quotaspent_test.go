package reconciler

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// A customer the kernel has cut off is out of data, though the packet that
// crossed the limit was dropped uncounted and their stored usage stops a
// few bytes short of it; one as close whose quota is not spent is not.
func TestACustomerTheKernelCutOffIsOut(t *testing.T) {
	r, db, enf, _ := newRig(t)
	const quota = 50 * 228
	cut := seed(t, db, model.Client{Name: "cut", QuotaBytes: quota, UsedBytes: quota - 4}, "10.66.0.2")
	near := seed(t, db, model.Client{Name: "near", QuotaBytes: quota, UsedBytes: quota - 4}, "10.66.0.3")
	enf.spent = map[string]uint64{enforce.Key(cut): quota}

	r.Tick(context.Background())

	var a, b model.Client
	db.First(&a, cut)
	db.First(&b, near)
	if a.Status != model.StatusExhausted {
		t.Fatalf("cut off by the kernel, but %s", a.Status)
	}
	if b.Status != model.StatusActive {
		t.Fatalf("not cut off, but %s", b.Status)
	}
}

// A customer just let back in -- a reset, a renewal -- is not cut off again
// by the kernel's quota from before it, which still holds the old period
// spent until the next apply writes it again.
func TestAResetIsNotUndoneByTheOldQuota(t *testing.T) {
	r, db, enf, _ := newRig(t)
	const quota = 50 * 228 // small enough that a fresh period is "near" it
	id := seed(t, db, model.Client{Name: "reset", QuotaBytes: quota, UsedBytes: 0}, "10.66.0.2")
	enf.spent = map[string]uint64{enforce.Key(id): quota} // last period's tally

	r.Tick(context.Background())

	var c model.Client
	db.First(&c, id)
	if c.Status != model.StatusActive {
		t.Fatalf("let back in, then cut off again by the old quota: %s", c.Status)
	}
}
