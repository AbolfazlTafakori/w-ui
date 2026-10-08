package service

import (
	"context"
	"testing"

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
