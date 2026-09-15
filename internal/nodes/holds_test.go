package nodes

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The push carries the plan's connections at once, and which devices the
// panel has decided to hold off -- so a node that missed the immediate call
// still learns of them.
func TestThePushCarriesTheLimitAndTheHolds(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusActive)
	if err := db.Model(&model.Client{}).Where("1 = 1").Update("device_limit", 3).Error; err != nil {
		t.Fatal(err)
	}
	var acc model.Account
	if err := db.First(&acc).Error; err != nil {
		t.Fatal(err)
	}

	s := newSyncer(db)
	until := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	s.Holds = func() map[uint]time.Time { return map[uint]time.Time{acc.ID: until} }

	states, err := s.desired(context.Background(), 7, false)
	if err != nil {
		t.Fatal(err)
	}
	c := states[0].Clients[0]
	if c.DeviceLimit != 3 {
		t.Fatalf("device limit %d sent, want 3", c.DeviceLimit)
	}
	if c.Accounts[0].HeldUntil == nil || !c.Accounts[0].HeldUntil.Equal(until) {
		t.Fatalf("hold not carried: %+v", c.Accounts[0].HeldUntil)
	}
}
