package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// The customer list, the online filter and the overview read what is
// happening now from the Live they are given, and agree with each other.
func TestTheListReadsWhatIsLiveNow(t *testing.T) {
	db := testDB(t)
	a := seed(t, db, 1)
	b := &model.Client{Name: "idle", Status: model.StatusActive}
	if err := db.Create(b).Error; err != nil {
		t.Fatal(err)
	}
	clients := NewClients(db, ipam.NewPools(), quietLog())
	clients.SetLive(fakeLive{
		conns:  map[uint]int{a.ID: 2},
		speeds: map[uint]model.Speed{a.ID: {Up: 5, Down: 50}},
		online: []uint{a.ID},
	})
	page, err := clients.List(context.Background(), ListFilter{PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range page.Items {
		switch c.ID {
		case a.ID:
			if c.OnlineNow != 2 || c.Speed == nil || c.Speed.Down != 50 {
				t.Errorf("the connected customer: online %d, speed %+v", c.OnlineNow, c.Speed)
			}
		case b.ID:
			if c.OnlineNow != 0 || c.Speed != nil {
				t.Errorf("the idle customer: online %d, speed %+v", c.OnlineNow, c.Speed)
			}
		}
	}
	online, err := clients.List(context.Background(), ListFilter{PerPage: 50, Buckets: []string{"online"}})
	if err != nil || online.Total != 1 || online.Items[0].ID != a.ID {
		t.Fatalf("the online filter: %+v, %v", online, err)
	}
	ov, err := clients.Overview(context.Background())
	if err != nil || ov.Online != 1 {
		t.Fatalf("the overview says %d online: %v", ov.Online, err)
	}
}

// A bulk change to plans tells the open subscription pages; a service told
// nothing tells nobody, and does not fall over for it.
func TestABulkChangeTellsTheOpenPages(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	clients := NewClients(db, ipam.NewPools(), quietLog())
	if _, err := clients.Extend(context.Background(), ExtendInput{IDs: []uint{c.ID}, Kind: ExtendTime, Days: 3}); err != nil {
		t.Fatalf("without Changes: %v", err)
	}
	told := 0
	changes := NewChanges()
	changes.Listen(func() { told++ })
	clients.SetChanges(changes)
	if _, err := clients.Extend(context.Background(), ExtendInput{IDs: []uint{c.ID}, Kind: ExtendTime, Days: 3}); err != nil {
		t.Fatal(err)
	}
	if told != 1 {
		t.Fatalf("told %d times", told)
	}
	var none *Changes
	none.Notify()
}
