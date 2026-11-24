package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A customer's configurations in one answer are, device by device, exactly
// what asking for each device gave -- hosts, file names and all -- and are
// narrowed to the caller's own customers as every read of one is.
func TestACustomersProfilesInOneAnswerAreEachDevicesOwn(t *testing.T) {
	p := newSeatPanel(t)
	ctx := context.Background()
	for i, name := range []string{"edge-a", "edge-b"} {
		if err := p.db.Create(&model.Host{InterfaceID: 1, Name: name, Address: "h.example.com", Port: 51820 + i, Enabled: true, Priority: i}).Error; err != nil {
			t.Fatal(err)
		}
	}
	c, err := p.clients.Create(ctx, CreateInput{Name: "many", InterfaceIDs: []uint{1, 2}, DeviceLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	all, err := p.clients.ClientProfiles(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	full, _ := p.clients.Get(ctx, c.ID)
	if len(all) != len(full.Accounts) || len(all) != 6 {
		t.Fatalf("%d devices answered for %d files", len(all), len(full.Accounts))
	}
	for _, d := range all {
		one, err := p.clients.Profiles(ctx, d.DeviceID)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(d.Profiles)
		b, _ := json.Marshal(one)
		if string(a) != string(b) {
			t.Fatalf("device %d differs:\n  together: %s\n  alone:    %s", d.DeviceID, a, b)
		}
	}

	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	if _, err := p.clients.ClientProfiles(asReseller(r, 1), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a reseller read the owner's customer's files: %v", err)
	}
}
