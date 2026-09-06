package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The filters have to narrow the list, and a filter that quietly narrows
// nothing is the failure worth guarding: it looks like it worked, and the
// operator acts on a list that still holds everything.
func TestTheFilterDrawerActuallyNarrowsTheList(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, nil, quietLog())

	past := time.Now().UTC().Add(-24 * time.Hour)
	soon := time.Now().UTC().Add(48 * time.Hour)

	seed := []model.Client{
		{Name: "active-plain", Protocol: model.ProtocolWireGuard, Status: model.StatusActive},
		{Name: "nearly-out", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
			QuotaBytes: 100, UsedBytes: 90},
		{Name: "used-up", Protocol: model.ProtocolWireGuard, Status: model.StatusExhausted},
		{Name: "switched-off", Protocol: model.ProtocolOpenVPN, Status: model.StatusDisabled,
			Group: "resellers", Note: "paid in cash"},
		{Name: "ran-out-of-time", Protocol: model.ProtocolOpenVPN, Status: model.StatusExpired,
			ExpiresAt: &past, ResetCycle: model.ResetMonthly},
		{Name: "expires-soon", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
			ExpiresAt: &soon, UsedBytes: 50 << 30},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].Name, err)
		}
	}

	names := func(f ListFilter) []string {
		t.Helper()
		f.PerPage = 100
		page, err := svc.List(context.Background(), f)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var out []string
		for _, c := range page.Items {
			out = append(out, c.Name)
		}
		return out
	}

	has := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, g := range got {
			seen[g] = true
		}
		for _, w := range want {
			if !seen[w] {
				return false
			}
		}
		return true
	}

	from50 := uint64(50 << 30)
	cases := []struct {
		name   string
		filter ListFilter
		want   []string
	}{
		{"nothing chosen leaves the list alone", ListFilter{}, []string{
			"active-plain", "nearly-out", "used-up", "switched-off",
			"ran-out-of-time", "expires-soon",
		}},
		// Depleting is not a status: it is an active customer far enough
		// through their allowance to be worth a renewal offer.
		{"depleting", ListFilter{Buckets: []string{"depleting"}}, []string{"nearly-out"}},
		// Several buckets mean any of them, not all.
		{"two buckets are an either/or", ListFilter{Buckets: []string{"expired", "disabled"}},
			[]string{"ran-out-of-time", "switched-off"}},
		{"protocol", ListFilter{Protocols: []model.Protocol{model.ProtocolOpenVPN}},
			[]string{"switched-off", "ran-out-of-time"}},
		{"group", ListFilter{Groups: []string{"resellers"}}, []string{"switched-off"}},
		{"has a note", ListFilter{HasNote: "yes"}, []string{"switched-off"}},
		{"renews", ListFilter{Renews: "on"}, []string{"ran-out-of-time"}},
		{"used at least 50GB", ListFilter{UsedFrom: &from50}, []string{"expires-soon"}},
		// Categories combine: a customer has to satisfy every one that was
		// filled in, not any of them.
		{"two categories narrow together",
			ListFilter{Buckets: []string{"active"}, Protocols: []model.Protocol{model.ProtocolWireGuard}, UsedFrom: &from50},
			[]string{"expires-soon"}},
	}

	for _, tc := range cases {
		if got := names(tc.filter); !has(got, tc.want...) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	// A bucket nobody recognises must leave the list alone rather than empty
	// it: a stale value in a saved URL should not look like "no matches".
	if got := names(ListFilter{Buckets: []string{"not-a-bucket"}}); len(got) != len(seed) {
		t.Errorf("an unknown bucket narrowed the list to %d of %d", len(got), len(seed))
	}
}

// Expiry is a range over a nullable column, and a customer with no expiry must
// not be swept into a range they were never in.
func TestExpiryRangeSkipsCustomersWithNoExpiry(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, nil, quietLog())

	soon := time.Now().UTC().Add(48 * time.Hour)
	for _, c := range []model.Client{
		{Name: "no-expiry", Protocol: model.ProtocolWireGuard, Status: model.StatusActive},
		{Name: "expires-soon", Protocol: model.ProtocolWireGuard, Status: model.StatusActive, ExpiresAt: &soon},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}

	from := time.Now().UTC()
	to := from.Add(7 * 24 * time.Hour)
	page, err := svc.List(context.Background(), ListFilter{ExpiryFrom: &from, ExpiryTo: &to, PerPage: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "expires-soon" {
		var got []string
		for _, c := range page.Items {
			got = append(got, c.Name)
		}
		t.Errorf("expiry range returned %v, want only expires-soon", got)
	}
}
