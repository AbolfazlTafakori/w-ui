package service

import (
	"context"
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
