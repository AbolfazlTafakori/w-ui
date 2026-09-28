package service

import (
	"strings"
	"testing"
)

// Two resellers cannot give their customers the same link. A link is one URL
// on the internet whoever sold it, and a shared one would hand one reseller's
// customer the other's files.
func TestTwoResellersCannotShareASubscriptionLink(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	sara := newReseller(t, admins, "sara", 0)

	if _, err := clients.Create(asReseller(reza, 1), CreateInput{
		Name: "rezas", InterfaceIDs: []uint{1}, SubID: "sharedlink123"}); err != nil {
		t.Fatal(err)
	}
	_, err := clients.Create(asReseller(sara, 1), CreateInput{
		Name: "saras", InterfaceIDs: []uint{1}, SubID: "sharedlink123"})
	if err == nil {
		t.Fatal("a second reseller was given a link another reseller's customer already has")
	}
	if strings.Contains(err.Error(), "rezas") || strings.Contains(err.Error(), "reza") {
		t.Fatalf("the refusal says whose link it is: %v", err)
	}
}

// Asking over and over whether a link exists is stopped: an existing link is
// a customer's private keys.
func TestProbingForLinksIsStopped(t *testing.T) {
	_, clients, admins := resellerDB(t)
	owner := t.Context()
	if _, err := clients.Create(owner, CreateInput{Name: "owners", InterfaceIDs: []uint{1}, SubID: "ownerslink0001"}); err != nil {
		t.Fatal(err)
	}
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)
	for i := 0; i < clashLimit; i++ {
		if _, err := clients.Create(ctx, CreateInput{Name: "probe", InterfaceIDs: []uint{1}, SubID: "ownerslink0001"}); err == nil {
			t.Fatal("a taken link was accepted")
		}
	}
	// Now even a free one is refused until the window passes.
	_, err := clients.Create(ctx, CreateInput{Name: "probe", InterfaceIDs: []uint{1}, SubID: "freelinkabcdef"})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("probing was not stopped: %v", err)
	}
	// Leaving it empty still works: a link is drawn.
	if _, err := clients.Create(ctx, CreateInput{Name: "honest", InterfaceIDs: []uint{1}}); err != nil {
		t.Fatalf("a customer with a drawn link was refused: %v", err)
	}
}

// Names and notes fit their columns, and carry no line breaks.
func TestNamesAreBoundedAndOnOneLine(t *testing.T) {
	_, clients, _ := resellerDB(t)
	ctx := t.Context()
	for name, in := range map[string]CreateInput{
		"long name":   {Name: strings.Repeat("a", maxClientName+1), InterfaceIDs: []uint{1}},
		"line break":  {Name: "a\nb", InterfaceIDs: []uint{1}},
		"long note":   {Name: "ok", Note: strings.Repeat("n", maxClientNote+1), InterfaceIDs: []uint{1}},
		"long device": {Name: "ok2", InterfaceIDs: []uint{1}, DeviceNames: []string{strings.Repeat("d", maxDeviceName+1)}},
	} {
		if _, err := clients.Create(ctx, in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	c, err := clients.Create(ctx, CreateInput{Name: strings.Repeat("ب", maxClientName), InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatalf("a name exactly the column's length, in Persian, was refused: %v", err)
	}
	bad := "x\ty"
	if _, err := clients.Update(ctx, c.ID, UpdateInput{Name: &bad}); err == nil {
		t.Error("a rename with a control character was accepted")
	}
	if _, err := clients.AddDevice(ctx, c.ID, strings.Repeat("d", maxDeviceName+1)); err == nil {
		t.Error("a device name past its column was accepted")
	}
}

// Importing over existing customers is refused to a paused reseller.
func TestImportIsRefusedToABarredReseller(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	if _, err := clients.Create(asReseller(reza, 1), CreateInput{Name: "theirs", InterfaceIDs: []uint{1}}); err != nil {
		t.Fatal(err)
	}
	sc := ScopeFor(reza)
	sc.Interfaces = map[uint]bool{1: true}
	sc.Barred = "your account's term has ended"
	_, err := clients.Import(WithScope(t.Context(), sc), ImportInput{
		InterfaceID: 1, OnConflict: ConflictReplace,
		Clients: []ClientRecord{{Name: "theirs", QuotaBytes: 500 << 30}},
	})
	if err == nil {
		t.Fatal("a paused reseller replaced their customers' plans by import")
	}
}
