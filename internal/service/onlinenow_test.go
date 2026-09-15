package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The number beside a customer is the connections in use, not the files
// issued: a credential live from two places is two, a switched-off device
// with an old address is none.
func TestListCountsConnectionsInUse(t *testing.T) {
	db := testDB(t)
	svc, _, made := seedServers(t, db, 2)
	now := time.Now().UTC()
	fresh, stale := now.Add(-30*time.Second), now.Add(-time.Hour)

	var accounts []model.Account
	if err := db.Where("client_id = ?", made[0].ID).Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	a := accounts[0]
	if err := db.Model(&model.Account{}).Where("id = ?", a.ID).Update("last_handshake", fresh).Error; err != nil {
		t.Fatal(err)
	}
	for _, e := range []model.AccountEndpoint{
		{AccountID: a.ID, Addr: "5.5.5.5", FirstSeen: fresh, LastSeen: fresh},
		{AccountID: a.ID, Addr: "6.6.6.6", FirstSeen: fresh, LastSeen: fresh},
		{AccountID: a.ID, Addr: "7.7.7.7", FirstSeen: stale, LastSeen: stale},
	} {
		if err := db.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The second customer has an address on record but no live handshake.
	if err := db.Where("client_id = ?", made[1].ID).Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AccountEndpoint{AccountID: accounts[0].ID, Addr: "8.8.8.8", FirstSeen: fresh, LastSeen: fresh}).Error; err != nil {
		t.Fatal(err)
	}

	page, err := svc.List(context.Background(), ListFilter{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint]int{}
	for _, c := range page.Items {
		got[c.ID] = c.OnlineNow
	}
	if got[made[0].ID] != 2 {
		t.Fatalf("customer live from two places counts %d", got[made[0].ID])
	}
	if got[made[1].ID] != 0 {
		t.Fatalf("customer with a stale handshake counts %d", got[made[1].ID])
	}
}
