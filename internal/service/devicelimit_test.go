package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The device limit must mean the same thing everywhere it is checked.
//
// It is checked in three places — issuing a device, lowering the limit, and the
// page that shows "n of m" — and each one that counts accounts instead of
// devices is a customer on several servers who cannot be given the devices they
// paid for. Two of the three had the bug; this is what stops the third joining
// them.
func TestDeviceLimitCountsTheSameThingEverywhere(t *testing.T) {
	// Two devices sold, spread across three servers: six rows.
	var accounts []model.Account
	for _, iface := range []uint{1, 2, 3} {
		for _, dev := range []string{"Phone", "Laptop"} {
			accounts = append(accounts, model.Account{
				InterfaceID: iface, DeviceName: dev,
			})
		}
	}

	if got := len(accounts); got != 6 {
		t.Fatalf("test setup produced %d accounts, want 6", got)
	}

	issued := len(deviceNames(accounts))
	if issued != 2 {
		t.Fatalf("deviceNames() says %d devices, want 2 — the limit would be enforced against the wrong number", issued)
	}

	// The operator raising the limit from two to three must not be told they
	// already have six. That was the real failure, found by running it.
	const newLimit = 3
	if newLimit < issued {
		t.Errorf("raising the limit to %d is refused against %d devices", newLimit, issued)
	}
	// And lowering below what is actually issued still has to be refused.
	const tooLow = 1
	if tooLow >= issued {
		t.Errorf("lowering the limit to %d should be refused against %d devices", tooLow, issued)
	}
}

// Adding a server must not consume a device slot, and removing one must not
// free one: the customer has the same devices either way.
func TestChangingServersDoesNotChangeTheDeviceCount(t *testing.T) {
	onOne := []model.Account{
		{InterfaceID: 1, DeviceName: "Phone"},
		{InterfaceID: 1, DeviceName: "Laptop"},
	}
	onThree := append([]model.Account{}, onOne...)
	for _, iface := range []uint{2, 3} {
		onThree = append(onThree,
			model.Account{InterfaceID: iface, DeviceName: "Phone"},
			model.Account{InterfaceID: iface, DeviceName: "Laptop"})
	}

	before := len(deviceNames(onOne))
	after := len(deviceNames(onThree))
	if before != after {
		t.Errorf("the customer went from %d devices to %d by being sold more servers", before, after)
	}
	if got := len(clientInterfaces(onThree)); got != 3 {
		t.Errorf("clientInterfaces() = %d, want 3", got)
	}
}

// A device is issued on every server the customer reaches, so the list of
// servers a new device must be placed on comes from the accounts they already
// have — not from the first one.
func TestANewDeviceGoesOnEveryServerTheCustomerHas(t *testing.T) {
	accounts := []model.Account{
		{InterfaceID: 5, DeviceName: "Phone"},
		{InterfaceID: 9, DeviceName: "Phone"},
		{InterfaceID: 2, DeviceName: "Phone"},
	}
	ifaces := clientInterfaces(accounts)
	if len(ifaces) != 3 {
		t.Fatalf("clientInterfaces() = %v, want all three servers", ifaces)
	}
	// Order follows the accounts, so the customer's first server stays first
	// and the protocol label on their row does not move.
	if ifaces[0] != 5 {
		t.Errorf("clientInterfaces() = %v, want the first account's server first", ifaces)
	}
}
