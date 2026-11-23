package service

import (
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An import that replaces a customer pays for what the replacement gives
// them, however the name in the file is written: in other letters' case,
// or twice in the one file. Found in review: the charge looked the names up
// exactly, the replacement case-blind, so "ALICE" grew alice for free.
func TestAnImportReplacingACustomerPaysForWhatItGives(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows func() []ClientRecord
	}{
		{"another case", func() []ClientRecord {
			return []ClientRecord{{Name: "ALICE", DeviceLimit: 3, QuotaBytes: 1 << 30, ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour))}}
		}},
		{"the same name twice", func() []ClientRecord {
			return []ClientRecord{
				{Name: "bob", DeviceLimit: 1, QuotaBytes: 1 << 30, ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour))},
				{Name: "bob", DeviceLimit: 3, QuotaBytes: 1 << 30, ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour))},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSeatPanel(t)
			r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 2, nil)
			ctx := asReseller(r, 1)
			p.make(t, ctx, "alice", 1)

			rep, err := p.clients.Import(ctx, ImportInput{InterfaceID: 1, OnConflict: ConflictReplace, Clients: tc.rows()})
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			acc := p.acc(t, r)
			var held int64
			p.db.Model(&model.Client{}).Where("owner_id = ? AND status = ?", r.ID, model.StatusActive).
				Select("COALESCE(SUM(device_limit), 0)").Scan(&held)
			if held > acc.Granted {
				t.Fatalf("customers hold %d users against %d granted (%+v)", held, acc.Granted, rep)
			}
			if acc.Used != held {
				t.Fatalf("the account says %d used, the customers hold %d (%+v)", acc.Used, held, rep)
			}
			if rep.Failed == 0 {
				t.Fatalf("a replacement past the ceiling was not refused: %+v", rep)
			}
			p.balanced(t)
		})
	}
}

// A customer brought in disabled holds no users, as one disabled by hand
// does not; turned back on, they take one, once.
func TestACustomerImportedDisabledHoldsNoUsers(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 2, nil)
	ctx := asReseller(r, 1)
	rep, err := p.clients.Import(ctx, ImportInput{InterfaceID: 1, Clients: []ClientRecord{
		{Name: "dora", DeviceLimit: 1, Status: string(model.StatusExpired), QuotaBytes: 1 << 30,
			ExpiresAt: ptrTime(time.Now().UTC().Add(-time.Hour))},
	}})
	if err != nil || rep.Created != 1 {
		t.Fatalf("import: %v %+v", err, rep)
	}
	if acc := p.acc(t, r); acc.Used != 0 {
		t.Fatalf("an expired customer brought in holds %d users", acc.Used)
	}
	p.balanced(t)
}
