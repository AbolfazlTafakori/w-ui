package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// One purchase, several servers. The interfaces a caller asked for are taken in
// the order they gave, with the single-server field folded in first so a caller
// that sells one server keeps working unchanged.
func TestChosenInterfaces(t *testing.T) {
	cases := []struct {
		name string
		in   CreateInput
		want []uint
	}{
		{"one server, the old field", CreateInput{InterfaceID: 3}, []uint{3}},
		{"several servers", CreateInput{InterfaceIDs: []uint{2, 5, 9}}, []uint{2, 5, 9}},
		{
			// The order decides which protocol is recorded on the customer's
			// row, so an operator who listed a tunnel first should see it first.
			name: "both fields, the single one leads",
			in:   CreateInput{InterfaceID: 7, InterfaceIDs: []uint{2, 7, 5}},
			want: []uint{7, 2, 5},
		},
		{"repeats are collapsed", CreateInput{InterfaceIDs: []uint{4, 4, 4}}, []uint{4}},
		{"zero is not a server", CreateInput{InterfaceID: 0, InterfaceIDs: []uint{0, 6}}, []uint{6}},
		{"nothing at all", CreateInput{}, []uint{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.chosenInterfaces()
			if len(got) != len(tc.want) {
				t.Fatalf("chosenInterfaces() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("chosenInterfaces() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// The device limit counts devices, not rows.
//
// A customer on three servers holds three accounts per device. Counting rows
// would refuse their second device on the grounds that they already have three,
// which is the bug this exists to stop coming back.
func TestDeviceNamesCountsDevicesNotAccounts(t *testing.T) {
	// Two devices across three servers: six accounts, two devices.
	var accounts []model.Account
	for _, iface := range []uint{1, 2, 3} {
		for _, dev := range []string{"Phone", "Laptop"} {
			accounts = append(accounts, model.Account{InterfaceID: iface, DeviceName: dev})
		}
	}

	devices := deviceNames(accounts)
	if len(devices) != 2 {
		t.Fatalf("deviceNames() found %d devices in %d accounts, want 2: %v",
			len(devices), len(accounts), devices)
	}
	if devices[0] != "Phone" || devices[1] != "Laptop" {
		t.Errorf("deviceNames() = %v, want them in the order they were issued", devices)
	}

	// And the servers, so a new device can be put on every one of them.
	ifaces := clientInterfaces(accounts)
	if len(ifaces) != 3 {
		t.Fatalf("clientInterfaces() = %v, want three servers", ifaces)
	}
}

// A device name that differs only in case is the same device. Two accounts
// called "Phone" and "phone" on one server would be two peers a customer
// thinks is one.
func TestDeviceNamesIsCaseInsensitive(t *testing.T) {
	got := deviceNames([]model.Account{
		{DeviceName: "Phone"}, {DeviceName: "phone"}, {DeviceName: "PHONE"},
	})
	if len(got) != 1 {
		t.Fatalf("deviceNames() = %v, want one device", got)
	}
	// The first spelling is the one that was issued, so it is the one kept.
	if got[0] != "Phone" {
		t.Errorf("deviceNames() kept %q, want the spelling it was created with", got[0])
	}
}

func TestDedupeKeepsFirstAndDropsZero(t *testing.T) {
	got := dedupe([]uint{3, 0, 1, 3, 1, 0, 7})
	want := []uint{3, 1, 7}
	if len(got) != len(want) {
		t.Fatalf("dedupe() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("dedupe() = %v, want %v", got, want)
		}
	}
}
