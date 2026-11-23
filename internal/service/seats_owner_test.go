package service

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A customer that changes hands leaves the old owner's units as a removal
// does and comes to the new owner's as a creation does, each named in the
// ledger as the transfer it was; one whose owner becomes the panel's owner
// is only given back.
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
	if m := got[5]; m.delta != -2 || m.spend != 0 || m.reason != model.CapacityGivenAway {
		t.Fatalf("old owner's move = %+v, want two users given back", m)
	}
	if m := got[7]; m.delta != 2 || m.spend != 2 || m.reason != model.CapacityTransferIn {
		t.Fatalf("new owner's move = %+v, want two users taken as a creation", m)
	}

	toOwner := planSeats(b, map[uint]seatState{1: {owner: 0, users: 2, alive: true}}, []uint{1})
	if len(toOwner) != 1 || toOwner[0].owner != 5 || toOwner[0].delta != -2 {
		t.Fatalf("handed to the owner: moves = %+v, want only the old owner's two back", toOwner)
	}
}

// A refusal for want of users counts them as a sentence does.
func TestARefusalForWantOfUsersCountsThemRight(t *testing.T) {
	open := &model.CapacityAccount{Mode: model.CapacityOpen, Granted: 5, Used: 5}
	if got := errNoUsers(open, 1).Error(); !strings.Contains(got, "needs 1 user at once") {
		t.Fatalf("one user: %q", got)
	}
	if got := errNoUsers(open, 2).Error(); !strings.Contains(got, "needs 2 users at once") {
		t.Fatalf("two users: %q", got)
	}
	used := &model.CapacityAccount{Mode: model.CapacityConsumable, Granted: 5, Used: 5}
	if got := errNoUsers(used, 1).Error(); !strings.Contains(got, "needs 1 user and") {
		t.Fatalf("one unit: %q", got)
	}
}
