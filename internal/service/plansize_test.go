package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// An allowance or a term no plan could mean is refused, on the field it is
// in, and never reaches the database as a bare SQL fault.
func TestAPlanPastWhatCanBeKeptIsRefusedOnItsField(t *testing.T) {
	_, clients, _ := resellerDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		in    CreateInput
		field string
	}{
		{"high bit", CreateInput{QuotaBytes: 1<<63 + 5}, "quotaBytes"},
		{"past a pebibyte", CreateInput{QuotaBytes: maxExtendData + 1}, "quotaBytes"},
		{"past a hundred years", CreateInput{StartOnFirstUse: true, DurationDays: maxPlanDays + 1}, "durationDays"},
	} {
		in := tc.in
		in.Name, in.DeviceLimit, in.InterfaceIDs = "x", 1, []uint{1}
		_, err := clients.Create(ctx, in)
		if !errors.Is(err, ErrInvalid) || FieldOf(err) != tc.field {
			t.Fatalf("%s: err = %v (field %q), want invalid on %s", tc.name, err, FieldOf(err), tc.field)
		}
	}

	c, err := clients.Create(ctx, CreateInput{Name: "ok", QuotaBytes: 1 << 30, DeviceLimit: 1, InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	big := uint64(maxExtendData + 1)
	if _, err := clients.Update(ctx, c.ID, UpdateInput{QuotaBytes: &big}); FieldOf(err) != "quotaBytes" {
		t.Fatalf("update past a pebibyte: err = %v, want refused on quotaBytes", err)
	}
	days := maxPlanDays + 1
	on := true
	if _, err := clients.Update(ctx, c.ID, UpdateInput{StartOnFirstUse: &on, DurationDays: &days}); FieldOf(err) != "durationDays" {
		t.Fatalf("update past a hundred years: err = %v, want refused on durationDays", err)
	}
}

// Many customers at the largest allowance do not take the overview down:
// their sum passes what a signed 64-bit integer holds.
func TestTheOverviewSurvivesAllowancesPastA64BitSum(t *testing.T) {
	db, clients, _ := resellerDB(t)
	ctx := context.Background()
	// Written straight in: 9000 pebibytes pass 2^63 bytes, and the overview
	// must still answer, however the rows came to be.
	for i := 0; i < 9000; i += 1000 {
		var rows []map[string]any
		for j := 0; j < 1000; j++ {
			rows = append(rows, map[string]any{"name": fmt.Sprintf("c%d", i+j), "quota_bytes": int64(maxExtendData),
				"status": "active", "protocol": "wireguard", "sub_token": fmt.Sprintf("t%d", i+j)})
		}
		if err := db.Table("clients").Create(rows).Error; err != nil {
			t.Fatal(err)
		}
	}
	o, err := clients.Overview(ctx)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.TotalQuota < 1<<63 {
		t.Fatalf("total quota = %d, want past 2^63", o.TotalQuota)
	}
}
