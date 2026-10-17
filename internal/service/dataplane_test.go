package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// fakeTunnels is a pool that refuses the tunnels named in fail.
type fakeTunnels struct {
	fail   map[string]bool
	opened []string
	closed []uint
}

func (f *fakeTunnels) Open(_ context.Context, iface *model.Interface) error {
	if f.fail[iface.Name] {
		return errors.New("will not come up")
	}
	f.opened = append(f.opened, iface.Name)
	return nil
}

func (f *fakeTunnels) Close(id uint) { f.closed = append(f.closed, id) }

// Stop marks this server's tunnels off, so the reconciler does not bring them
// back; start turns them on again; another server's tunnels are not touched,
// and one tunnel that will not come up does not stop the rest.
func TestStopAndStartEveryTunnelOnThisServer(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for i, iface := range []model.Interface{
		{Name: "wg0", NodeID: 1, Enabled: true},
		{Name: "wg1", NodeID: 1, Enabled: true},
		{Name: "far0", NodeID: 2, Enabled: true},
	} {
		iface.Protocol, iface.ListenPort = model.ProtocolWireGuard, 51820+i
		iface.Subnet = []string{"10.1.0.0/24", "10.2.0.0/24", "10.3.0.0/24"}[i]
		if err := db.Create(&iface).Error; err != nil {
			t.Fatal(err)
		}
	}
	pool := &fakeTunnels{fail: map[string]bool{"wg1": true}}
	dp := NewDataPlane(db, pool, 1, quietLog())

	res, err := dp.StopAll(ctx)
	if err != nil || res.Acted != 2 || len(pool.closed) != 2 {
		t.Fatalf("stop = %+v, %v; closed %v", res, err, pool.closed)
	}
	var on int64
	db.Model(&model.Interface{}).Where("enabled = ?", true).Count(&on)
	if on != 1 {
		t.Fatalf("%d tunnels left on; only the other server's should be", on)
	}

	res, err = dp.StartAll(ctx)
	if err != nil || res.Acted != 1 || res.Failures["wg1"] == nil {
		t.Fatalf("start = %+v, %v", res, err)
	}
	db.Model(&model.Interface{}).Where("enabled = ?", true).Count(&on)
	if on != 3 {
		t.Fatalf("%d tunnels on after start; a tunnel that would not open is still switched on", on)
	}

	pool.opened = nil
	res, err = dp.RestartAll(ctx)
	if err != nil || res.Acted != 1 || len(res.Failures) != 1 || len(pool.opened) != 1 || pool.opened[0] != "wg0" {
		t.Fatalf("restart = %+v, %v; opened %v", res, err, pool.opened)
	}
}

// A term on hold starts at the first sign-in, once: a second sign-in does
// not move it.
func TestAResellersTermStartsAtTheFirstSignIn(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	admins := NewAdmins(db, quietLog())
	a := model.Admin{Username: "res", Role: model.RoleReseller, Enabled: true, DurationDays: 30}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	admins.SignedIn(ctx, &a, "198.51.100.4", now)
	if a.ExpiresAt == nil || !a.ExpiresAt.Equal(now.Add(30*24*time.Hour)) || a.DurationDays != 0 {
		t.Fatalf("after the first sign-in: expires %v, days %d", a.ExpiresAt, a.DurationDays)
	}
	stored, err := admins.ByUsername(ctx, "res")
	if err != nil || stored.ExpiresAt == nil || stored.LastLoginIP != "198.51.100.4" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	first := *stored.ExpiresAt
	admins.SignedIn(ctx, stored, "198.51.100.5", now.Add(48*time.Hour))
	again, _ := admins.ByUsername(ctx, "res")
	if !again.ExpiresAt.Equal(first) {
		t.Fatalf("a second sign-in moved the term: %v, was %v", again.ExpiresAt, first)
	}
	if _, err := admins.ByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no such operator: %v", err)
	}
	if _, err := admins.ForSession(ctx, "not a number"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a session naming no number: %v", err)
	}
}
