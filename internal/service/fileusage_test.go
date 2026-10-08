package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Every way a customer's usage is reset takes their files' counters with
// it, and the direction split too, so the subscription page never sets last
// period's per-user table beside this period's usage.
func TestResetsClearTheFilesToo(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(svc *Clients, c *model.Client) error{
		"one customer": func(svc *Clients, c *model.Client) error {
			_, err := svc.ResetTraffic(ctx, c.ID)
			return err
		},
		"in bulk": func(svc *Clients, c *model.Client) error {
			_, err := svc.Bulk(ctx, BulkReset, []uint{c.ID})
			return err
		},
		"everyone": func(svc *Clients, c *model.Client) error {
			_, err := svc.ResetAllTraffic(ctx)
			return err
		},
	}
	for name, reset := range cases {
		t.Run(name, func(t *testing.T) {
			db := testDB(t)
			c := seed(t, db, 2)
			if err := db.Model(&model.Client{}).Where("id = ?", c.ID).
				Update("used_bytes", 3<<30).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.Account{}).Where("client_id = ?", c.ID).
				UpdateColumns(map[string]any{"up_bytes": 1 << 20, "down_bytes": 5 << 20}).Error; err != nil {
				t.Fatal(err)
			}

			if err := reset(NewClients(db, nil, quietLog()), c); err != nil {
				t.Fatalf("reset: %v", err)
			}

			var after model.Client
			db.First(&after, c.ID)
			if after.UsedBytes != 0 || after.UpBytes != 0 || after.DownBytes != 0 {
				t.Fatalf("customer counters left behind: used %d up %d down %d",
					after.UsedBytes, after.UpBytes, after.DownBytes)
			}
			var accs []model.Account
			db.Where("client_id = ?", c.ID).Find(&accs)
			for _, a := range accs {
				if a.UpBytes != 0 || a.DownBytes != 0 {
					t.Fatalf("file %s kept last period: up %d down %d", a.DeviceName, a.UpBytes, a.DownBytes)
				}
			}
		})
	}
}

// A customer whose period began before files were counted is told from when
// their per-user table counts; one whose period began after is not, because
// their table adds up to their usage.
func TestThePageSaysFromWhenFilesAreCounted(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	since := time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC)
	if err := database.PutSetting(db, database.KeyFileUsageSince, since.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	subs := newSubs(db, nil)
	ctx := context.Background()

	set := func(created time.Time, reset *time.Time, used uint64) *model.Client {
		if err := db.Model(&model.Client{}).Where("id = ?", c.ID).UpdateColumns(map[string]any{
			"created_at": created, "last_reset_at": reset, "used_bytes": used,
		}).Error; err != nil {
			t.Fatal(err)
		}
		var got model.Client
		db.First(&got, c.ID)
		return &got
	}

	if got := subs.filesSince(ctx, set(since.Add(-48*time.Hour), nil, 1<<30)); got == nil || !got.Equal(since) {
		t.Errorf("a period from before the start: got %v, want %v", got, since)
	}
	after := since.Add(time.Hour)
	if got := subs.filesSince(ctx, set(since.Add(-48*time.Hour), &after, 1<<30)); got != nil {
		t.Errorf("a period reset after the start covers it all, got %v", got)
	}
	if got := subs.filesSince(ctx, set(since.Add(time.Minute), nil, 1<<30)); got != nil {
		t.Errorf("a customer created after the start, got %v", got)
	}
	if got := subs.filesSince(ctx, set(since.Add(-48*time.Hour), nil, 0)); got != nil {
		t.Errorf("nothing used, nothing to explain, got %v", got)
	}
}
