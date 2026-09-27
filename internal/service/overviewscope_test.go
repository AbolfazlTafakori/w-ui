package service

import "testing"

// The tiles over a reseller's customer list count their own customers, and
// only those.
//
// The customer list asks for these in the same breath as the rows, so they
// have to be answerable by a reseller -- but customers, devices, tunnels and
// who is online are four different tables, and only the first is narrowed for
// us. Handing over the panel's totals would tell a reseller how big the
// machine they rent a corner of is, and how many people are on it.
func TestTheOverviewCountsOnlyWhatTheOperatorHolds(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	sara := newReseller(t, admins, "sara", 0)

	// Two customers for one, one for the other, with devices behind them.
	for _, name := range []string{"reza-one", "reza-two"} {
		if _, err := clients.Create(asReseller(reza, 1),
			CreateInput{Name: name, InterfaceIDs: []uint{1}, DeviceLimit: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := clients.Create(asReseller(sara, 1),
		CreateInput{Name: "sara-one", InterfaceIDs: []uint{1}, DeviceLimit: 2}); err != nil {
		t.Fatal(err)
	}

	owner, err := clients.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if owner.Clients != 3 {
		t.Fatalf("the owner counts %d customers, want all 3", owner.Clients)
	}

	mine, err := clients.Overview(asReseller(reza, 1))
	if err != nil {
		t.Fatal(err)
	}
	if mine.Clients != 2 {
		t.Errorf("a reseller is told they hold %d customers, want their own 2", mine.Clients)
	}
	if mine.Devices >= owner.Devices {
		t.Errorf("a reseller is told there are %d devices and the panel has %d: "+
			"they are being shown the machine's total", mine.Devices, owner.Devices)
	}
	// Two customers, each allowed two devices, and a device is an account on
	// every tunnel they may reach.
	if mine.Devices != 4 {
		t.Errorf("a reseller is told they have %d devices, want their own 4", mine.Devices)
	}

	// Tunnels means the ones they were given to sell, not the ones the
	// machine runs. The fixture has two; this reseller was given one.
	if mine.Interfaces != 1 {
		t.Errorf("a reseller is told there are %d tunnels, want the 1 they may sell",
			mine.Interfaces)
	}
	if owner.Interfaces != 2 {
		t.Errorf("the owner is told there are %d tunnels, want both", owner.Interfaces)
	}

	// And the traffic sold is theirs, not the panel's.
	if mine.TotalUsed > owner.TotalUsed {
		t.Errorf("a reseller is credited with more traffic than the panel has carried")
	}
}
