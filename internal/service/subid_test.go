package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// A device's file is named after the customer, and a WireGuard file as a
// WireGuard app takes it: ASCII letters, digits and _ = + . -, at most
// fifteen, with what tells it from the customer's other files kept whole.
func TestFilesAreNamedAfterTheCustomer(t *testing.T) {
	for _, tc := range []struct {
		client, tunnel, base string
		parts                []string
		want                 string
	}{
		{"Hossein", "wg0", "device-1.conf", nil, "Hossein.conf"},
		{"Hossein", "wg0", "laptop.conf", []string{"laptop"}, "Hossein-laptop.conf"},
		{"Hossein", "wg0", "device-1.ovpn", nil, "Hossein.ovpn"},
		{"حسین رضایی", "ovpn0", "device-1.ovpn", nil, "حسین-رضایی.ovpn"},
		// Another script: named by the tunnel, which the app takes.
		{"حسین رضایی", "awg1", "device-1.conf", nil, "awg1.conf"},
		{"حسین رضایی", "awg1", "device-1.conf", []string{"awg1", "phone"}, "awg1-phone.conf"},
		// Cut from the customer's name, never from what tells files apart.
		{"a very long customer name", "wg0", "phone.conf", []string{"phone"}, "a-very-lo-phone.conf"},
		{"l2tp-ui-test", "awg1", "x.conf", []string{"awg1"}, "l2tp-ui-te-awg1.conf"},
		{"l2tp-ui-test", "awg2", "x.conf", []string{"awg2"}, "l2tp-ui-te-awg2.conf"},
		{"a very long customer name", "wg0", "phone.ovpn", []string{"phone"}, "a-very-long-customer-name-phone.ovpn"},
		{"\"; rm -rf /", "wg0", "d.conf", nil, "rm--rf.conf"},
		{"Ali", "wg0", "d.conf", []string{"Germany 2"}, "Ali-Germany-2.conf"},
	} {
		got := deviceFilename(tc.client, tc.tunnel, tc.base, tc.parts...)
		if got != tc.want {
			t.Errorf("deviceFilename(%q, %q, %q, %q) = %q, want %q", tc.client, tc.tunnel, tc.base, tc.parts, got, tc.want)
		}
		if strings.HasSuffix(got, ".conf") {
			name := strings.TrimSuffix(got, ".conf")
			if len(name) > confNameMax || confName(name) != name {
				t.Errorf("%q is not a name a WireGuard app takes", got)
			}
		}
	}
}

// A plan for n users is n files: one each, issued with the plan, added when
// the plan grows, the newest removed when it shrinks.
func TestAPlanForNUsersIsNFiles(t *testing.T) {
	db := testDB(t)
	svc, ifaces, _ := seedServers(t, db, 0)
	c, err := svc.Create(context.Background(), CreateInput{
		Name: "Roya", InterfaceIDs: []uint{ifaces[0].ID}, DeviceLimit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		got, err := svc.Get(context.Background(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return deviceNames(got.Accounts)
	}
	if n := names(); len(n) != 3 || n[0] != "user-1" || n[2] != "user-3" {
		t.Fatalf("files for a plan of three: %v", n)
	}
	five := 5
	if _, err := svc.Update(context.Background(), c.ID, UpdateInput{DeviceLimit: &five}); err != nil {
		t.Fatal(err)
	}
	if n := names(); len(n) != 5 || n[4] != "user-5" {
		t.Fatalf("files after growing to five: %v", n)
	}
	one := 1
	if _, err := svc.Update(context.Background(), c.ID, UpdateInput{DeviceLimit: &one}); err != nil {
		t.Fatal(err)
	}
	if n := names(); len(n) != 1 || n[0] != "user-1" {
		t.Fatalf("files after shrinking to one: %v", n)
	}
	// A single-user plan is one file with the plain name.
	d, err := svc.Create(context.Background(), CreateInput{Name: "Sina", InterfaceIDs: []uint{ifaces[0].ID}, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := deviceNames(d.Accounts); len(n) != 1 || n[0] != "device-1" {
		t.Fatalf("files for a plan of one: %v", n)
	}
	// Unlimited leaves the files alone.
	zero := 0
	if _, err := svc.Update(context.Background(), c.ID, UpdateInput{DeviceLimit: &zero}); err != nil {
		t.Fatal(err)
	}
	if n := names(); len(n) != 1 {
		t.Fatalf("files after unlimited: %v", n)
	}
}
