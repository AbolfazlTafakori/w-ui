package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// One customer's page says which of their files are connected now by the
// reading the list counts with, and none for a customer switched off.
func TestEachFileSaysWhetherItIsOnline(t *testing.T) {
	p := newSeatPanel(t)
	c := p.make(t, context.Background(), "two", 2)
	full, err := p.clients.Get(context.Background(), c.ID)
	if err != nil || len(full.Accounts) != 2 {
		t.Fatalf("%v %d", err, len(full.Accounts))
	}
	first, second := full.Accounts[0].ID, full.Accounts[1].ID

	// Without the live reading: a handshake inside the window.
	recent := time.Now().UTC().Add(-time.Minute)
	p.db.Model(&model.Account{}).Where("id = ?", first).Update("last_handshake", recent)
	got := online(t, p, c.ID)
	if !got[first] || got[second] {
		t.Fatalf("by handshake: %v", got)
	}

	// With it, the live reading decides, whatever the handshake says.
	p.clients.SetLive(fakeLive{live: map[uint]bool{second: true}})
	got = online(t, p, c.ID)
	if got[first] || !got[second] {
		t.Fatalf("live: %v", got)
	}

	p.db.Model(&model.Client{}).Where("id = ?", c.ID).Update("status", model.StatusDisabled)
	got = online(t, p, c.ID)
	if got[first] || got[second] {
		t.Fatalf("a customer switched off has files online: %v", got)
	}
}

func online(t *testing.T, p seatPanel, id uint) map[uint]bool {
	t.Helper()
	c, err := p.clients.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uint]bool{}
	for _, a := range c.Accounts {
		out[a.ID] = a.Online
	}
	return out
}
