package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A customer that changes hands leaves the old owner's units as a removal
// does and comes to the new owner's as a creation does; one whose owner
// becomes the panel's owner is only given back.
func TestACustomerChangingHandsMovesTheirUsersWithThem(t *testing.T) {
	b := map[uint]seatState{1: {owner: 5, users: 2, alive: true}}
	a := map[uint]seatState{1: {owner: 7, users: 2, alive: true}}
	moves := planSeats(b, a, []uint{1})
	if len(moves) != 2 {
		t.Fatalf("moves = %+v, want one for each owner", moves)
	}
	got := map[uint]seatMove{}
	for _, m := range moves {
		got[m.owner] = m
	}
	if m := got[5]; m.delta != -2 || m.spend != 0 || m.reason != model.CapacityRelease {
		t.Fatalf("old owner's move = %+v, want two users given back", m)
	}
	if m := got[7]; m.delta != 2 || m.spend != 2 || m.reason != model.CapacityCreate {
		t.Fatalf("new owner's move = %+v, want two users taken as a creation", m)
	}

	toOwner := planSeats(b, map[uint]seatState{1: {owner: 0, users: 2, alive: true}}, []uint{1})
	if len(toOwner) != 1 || toOwner[0].owner != 5 || toOwner[0].delta != -2 {
		t.Fatalf("handed to the owner: moves = %+v, want only the old owner's two back", toOwner)
	}
}
