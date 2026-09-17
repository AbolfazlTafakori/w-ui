package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The subscription id is the operator's to choose, so a customer carried
// over from elsewhere keeps a working link; left empty one is drawn, and
// no two customers share one.
func TestSubscriptionIDIsChosenOrDrawnAndUnique(t *testing.T) {
	db := testDB(t)
	svc, ifaces, _ := seedServers(t, db, 0)
	expires := time.Now().Add(24 * time.Hour)
	mk := func(name, sub string) (*subIDResult, error) {
		c, err := svc.Create(context.Background(), CreateInput{
			Name: name, InterfaceIDs: []uint{ifaces[0].ID}, ExpiresAt: &expires, DeviceLimit: 1, SubID: sub,
		})
		if err != nil {
			return nil, err
		}
		return &subIDResult{id: c.ID, sub: c.SubToken}, nil
	}
	a, err := mk("Roya", "roya-link-2024")
	if err != nil {
		t.Fatal(err)
	}
	if a.sub != "roya-link-2024" {
		t.Fatalf("chosen id not kept: %q", a.sub)
	}
	b, err := mk("Sina", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.sub) < 16 {
		t.Fatalf("no id drawn: %q", b.sub)
	}
	if _, err := mk("Mina", "roya-link-2024"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a duplicate id was accepted: %v", err)
	}
	for _, bad := range []string{"short", "has space in it", "bad/char!"} {
		if _, err := mk("X"+bad, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad id %q accepted: %v", bad, err)
		}
	}
	// Changed later, the old link is gone and the new one is unique too.
	if _, err := svc.Update(context.Background(), b.id, UpdateInput{SubID: "roya-link-2024"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("taking another customer's id was accepted: %v", err)
	}
	got, err := svc.Update(context.Background(), b.id, UpdateInput{SubID: "sina-new-link"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SubToken != "sina-new-link" {
		t.Fatalf("id after update: %q", got.SubToken)
	}
	// Saving the same id back is not a change.
	if _, err := svc.Update(context.Background(), b.id, UpdateInput{SubID: "sina-new-link"}); err != nil {
		t.Fatal(err)
	}
}

type subIDResult struct {
	id  uint
	sub string
}

// Empty means unlimited, and stays so on an edit: an expiry can be cleared,
// a quota set back to nothing, the connections limit lifted.
func TestEmptyMeansUnlimitedOnEditToo(t *testing.T) {
	db := testDB(t)
	svc, ifaces, _ := seedServers(t, db, 0)
	expires := time.Now().Add(24 * time.Hour)
	c, err := svc.Create(context.Background(), CreateInput{
		Name: "Roya", InterfaceIDs: []uint{ifaces[0].ID}, ExpiresAt: &expires, QuotaBytes: 5 << 30, DeviceLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	zero := uint64(0)
	none := 0
	got, err := svc.Update(context.Background(), c.ID, UpdateInput{ExpiresAt: ClearTime(), QuotaBytes: &zero, DeviceLimit: &none})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt != nil || got.QuotaBytes != 0 || got.DeviceLimit != 0 {
		t.Fatalf("after clearing: expires=%v quota=%d limit=%d", got.ExpiresAt, got.QuotaBytes, got.DeviceLimit)
	}
	// An edit that says nothing about the date leaves it alone.
	later := time.Now().Add(48 * time.Hour)
	if _, err := svc.Update(context.Background(), c.ID, UpdateInput{ExpiresAt: At(later)}); err != nil {
		t.Fatal(err)
	}
	got, err = svc.Update(context.Background(), c.ID, UpdateInput{QuotaBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("an edit without a date cleared it")
	}
	// And the wire form: null clears, absence keeps.
	var in UpdateInput
	if err := json.Unmarshal([]byte(`{"expiresAt":null}`), &in); err != nil || !in.ExpiresAt.Set || in.ExpiresAt.Value != nil {
		t.Fatalf("null on the wire: %+v %v", in.ExpiresAt, err)
	}
	in = UpdateInput{}
	if err := json.Unmarshal([]byte(`{"name":"x"}`), &in); err != nil || in.ExpiresAt.Set {
		t.Fatalf("absence on the wire: %+v %v", in.ExpiresAt, err)
	}
	// A plan with no connections limit is a limit of none.
	if in.DeviceLimit != nil {
		t.Fatal("absent limit decoded as set")
	}
}

// A device's file is named after the customer, in their own script, and a
// WireGuard file keeps to the fifteen characters the app allows.
func TestFilesAreNamedAfterTheCustomer(t *testing.T) {
	for _, tc := range []struct {
		client, device, base string
		single               bool
		want                 string
	}{
		{"Hossein", "device-1", "device-1.conf", true, "Hossein.conf"},
		{"Hossein", "laptop", "laptop.conf", false, "Hossein-laptop.conf"},
		{"Hossein", "device-1", "device-1.ovpn", true, "Hossein.ovpn"},
		{"حسین رضایی", "device-1", "device-1.ovpn", true, "حسین-رضایی.ovpn"},
		{"a very long customer name", "phone", "phone.conf", false, "a-very-long-cus.conf"},
		{"a very long customer name", "phone", "phone.ovpn", false, "a-very-long-customer-name-phone.ovpn"},
		{"\"; rm -rf /", "d", "d.conf", true, "rm--rf.conf"},
	} {
		if got := clientFilename(tc.client, tc.device, tc.base, tc.single); got != tc.want {
			t.Errorf("clientFilename(%q, %q, %q, %v) = %q, want %q", tc.client, tc.device, tc.base, tc.single, got, tc.want)
		}
	}
}
