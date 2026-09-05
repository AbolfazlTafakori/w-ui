package service

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func seedHosts(t *testing.T, db *gorm.DB, n int) []model.Host {
	t.Helper()

	var made []model.Host
	for i := 0; i < n; i++ {
		h := model.Host{
			InterfaceID: 1,
			Name:        string(rune('a' + i)),
			Address:     "vpn.example.com",
			Enabled:     true,
			Priority:    i + 1,
		}
		if err := db.Create(&h).Error; err != nil {
			t.Fatal(err)
		}
		made = append(made, h)
	}
	return made
}

func hostIDs(hs []model.Host) []uint {
	out := make([]uint, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

// Addresses are added and edited in a hurry, when something is already blocked.
// Four dialogs to switch off four addresses is three too many.
func TestSeveralAddressesAreSwitchedOffAtOnce(t *testing.T) {
	db := testDB(t)
	svc := NewHosts(db, quietLog())
	hosts := seedHosts(t, db, 3)

	n, err := svc.Bulk(context.Background(), HostDisable, hostIDs(hosts))
	if err != nil {
		t.Fatalf("Bulk: %v", err)
	}
	if n != 3 {
		t.Errorf("disabled %d addresses, want 3", n)
	}

	var enabled int64
	if err := db.Model(&model.Host{}).Where("enabled = ?", true).Count(&enabled).Error; err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Errorf("%d addresses are still enabled", enabled)
	}
}

func TestSeveralAddressesAreDeletedAtOnce(t *testing.T) {
	db := testDB(t)
	svc := NewHosts(db, quietLog())
	hosts := seedHosts(t, db, 3)

	if _, err := svc.Bulk(context.Background(), HostDelete, hostIDs(hosts)[:2]); err != nil {
		t.Fatalf("Bulk: %v", err)
	}

	var left int64
	if err := db.Model(&model.Host{}).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Errorf("%d addresses left, want 1", left)
	}
}

// Nothing selected is a mistake about the request, not a silent no-op.
func TestABulkWithNothingSelectedIsRefused(t *testing.T) {
	db := testDB(t)
	svc := NewHosts(db, quietLog())

	if _, err := svc.Bulk(context.Background(), HostDisable, nil); err == nil {
		t.Error("a bulk action with nothing selected was accepted")
	}
	if _, err := svc.Bulk(context.Background(), "explode", []uint{1}); err == nil {
		t.Error("an action that is not one of ours was accepted")
	}
}

// Which address a customer is handed first is the whole point of the ordering,
// and an operator who has worked out which is fastest should be able to say so
// without doing arithmetic on priority numbers.
func TestTheOrderSentIsTheOrderStored(t *testing.T) {
	db := testDB(t)
	svc := NewHosts(db, quietLog())
	hosts := seedHosts(t, db, 3)

	// Reversed: the last becomes the first.
	want := []uint{hosts[2].ID, hosts[0].ID, hosts[1].ID}
	if _, err := svc.Reorder(context.Background(), want); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	var got []model.Host
	if err := db.Order("priority").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	for i, h := range got {
		if h.ID != want[i] {
			t.Errorf("position %d holds host %d, want %d", i, h.ID, want[i])
		}
		if h.Priority != i+1 {
			t.Errorf("host %d has priority %d, want %d", h.ID, h.Priority, i+1)
		}
	}
}

// Numbered from one with no gaps, so the next address added has somewhere
// obvious to go and the numbers stay readable.
func TestReorderingLeavesNoGaps(t *testing.T) {
	db := testDB(t)
	svc := NewHosts(db, quietLog())
	hosts := seedHosts(t, db, 4)

	if _, err := svc.Reorder(context.Background(), hostIDs(hosts)); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	var got []model.Host
	if err := db.Order("priority").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	for i, h := range got {
		if h.Priority != i+1 {
			t.Fatalf("priorities are %v, want 1..%d", priorities(got), len(got))
		}
	}
}

func priorities(hs []model.Host) []int {
	out := make([]int, len(hs))
	for i, h := range hs {
		out[i] = h.Priority
	}
	return out
}
