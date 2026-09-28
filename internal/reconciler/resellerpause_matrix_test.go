package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"gorm.io/gorm"
)

// pauseRig is a reseller with a customer in every state, a second reseller in
// good standing, and a customer of the owner's own -- every row the pause of
// one reseller could wrongly reach.
type pauseRig struct {
	r      *Reconciler
	db     *gorm.DB
	enf    *fakeEnforcer
	drv    *backend.Memory
	paused model.Admin
	other  model.Admin
	// by name, the customers and the addresses each of their devices holds
	ids   map[string]uint
	addrs map[string][]string
}

func newPauseRig(t *testing.T) *pauseRig {
	t.Helper()
	r, db, enf, drv := newRig(t)
	p := &pauseRig{r: r, db: db, enf: enf, drv: drv, ids: map[string]uint{}, addrs: map[string][]string{}}

	p.paused = model.Admin{Username: "reza", PasswordHash: "x", Role: model.RoleReseller, Enabled: true}
	p.other = model.Admin{Username: "sara", PasswordHash: "x", Role: model.RoleReseller, Enabled: true}
	for _, a := range []*model.Admin{&p.paused, &p.other} {
		if err := db.Create(a).Error; err != nil {
			t.Fatal(err)
		}
	}

	next := 2
	add := func(name string, owner uint, status model.ClientStatus, devices int) {
		ip := fmt.Sprintf("10.66.0.%d", next)
		next++
		id := seed(t, db, model.Client{Name: name, Status: status, DeviceLimit: devices}, ip)
		p.ids[name] = id
		p.addrs[name] = []string{ip}
		for d := 2; d <= devices; d++ {
			extra := fmt.Sprintf("10.66.0.%d", next)
			next++
			acc := model.Account{ClientID: id, InterfaceID: 1, NodeID: 1,
				DeviceName: fmt.Sprintf("device-%d", d), IP: extra, PublicKey: "pk-" + extra, Enabled: true}
			if err := db.Create(&acc).Error; err != nil {
				t.Fatal(err)
			}
			p.addrs[name] = append(p.addrs[name], extra)
		}
		if owner != 0 {
			db.Model(&model.Client{}).Where("id = ?", id).Update("owner_id", owner)
		}
	}
	add("active", p.paused.ID, model.StatusActive, 1)
	add("three-devices", p.paused.ID, model.StatusActive, 3)
	add("disabled", p.paused.ID, model.StatusDisabled, 1)
	add("expired", p.paused.ID, model.StatusExpired, 1)
	add("exhausted", p.paused.ID, model.StatusExhausted, 1)
	add("other-resellers", p.other.ID, model.StatusActive, 1)
	add("owners", 0, model.StatusActive, 1)
	return p
}

// served reports, for every customer, whether the kernel lets them through
// and the tunnel carries every one of their devices. Both halves have to
// agree: a peer left behind a blocking rule, or a rule letting through
// traffic for a peer that is gone, is a customer half-served.
func (p *pauseRig) served(t *testing.T) map[string]bool {
	t.Helper()
	onPeer := map[uint]bool{}
	for _, id := range p.drv.Accounts() {
		onPeer[id] = true
	}
	out := map[string]bool{}
	for name, id := range p.ids {
		rule, ok := p.enf.ruleFor(enforce.Key(id))
		if !ok {
			t.Fatalf("%s has no kernel rule", name)
		}
		kernel := !rule.Blocked
		var devices []uint
		p.db.Model(&model.Account{}).Where("client_id = ?", id).Pluck("id", &devices)
		if len(devices) != len(p.addrs[name]) {
			t.Fatalf("%s has %d devices on record, the fixture made %d", name, len(devices), len(p.addrs[name]))
		}
		onTunnel := 0
		for _, d := range devices {
			if onPeer[d] {
				onTunnel++
			}
		}
		switch {
		case kernel && onTunnel != len(devices):
			t.Fatalf("%s is let through the kernel with %d of %d devices on the tunnel",
				name, onTunnel, len(devices))
		case !kernel && onTunnel != 0:
			t.Fatalf("%s is blocked in the kernel but %d device(s) are still on the tunnel", name, onTunnel)
		}
		out[name] = kernel
	}
	return out
}

func (p *pauseRig) statuses() map[string]model.ClientStatus {
	out := map[string]model.ClientStatus{}
	for name, id := range p.ids {
		var c model.Client
		p.db.First(&c, id)
		out[name] = c.Status
	}
	return out
}

// What each customer's own switch says, which is what they must come back to.
var ownSwitch = map[string]bool{
	"active": true, "three-devices": true, "disabled": false,
	"expired": false, "exhausted": false, "other-resellers": true, "owners": true,
}

// Every way a reseller is paused, against every state a customer of theirs
// can be in.
func TestEveryPauseAgainstEveryCustomerState(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	reasons := []struct {
		name   string
		pause  map[string]any
		resume map[string]any
	}{
		{"switched off", map[string]any{"enabled": false}, map[string]any{"enabled": true}},
		{"term ended", map[string]any{"expires_at": past}, map[string]any{"expires_at": future}},
		{"traffic used", map[string]any{"quota_bytes": 1000, "used_bytes": 1000}, map[string]any{"used_bytes": 0}},
		{"switched off and term ended", map[string]any{"enabled": false, "expires_at": past},
			map[string]any{"enabled": true, "expires_at": future}},
	}
	for _, reason := range reasons {
		t.Run(reason.name, func(t *testing.T) {
			p := newPauseRig(t)
			ctx := context.Background()

			p.r.Tick(ctx)
			if got := p.served(t); !equal(got, ownSwitch) {
				t.Fatalf("before the pause: %v, want each customer's own switch %v", got, ownSwitch)
			}
			before := p.statuses()

			p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Updates(reason.pause)
			p.r.Tick(ctx)
			want := map[string]bool{}
			for name := range ownSwitch {
				want[name] = false
			}
			want["other-resellers"], want["owners"] = true, true
			if got := p.served(t); !equal(got, want) {
				t.Fatalf("while paused: %v, want %v", got, want)
			}
			if after := p.statuses(); !equalStatus(after, before) {
				t.Fatalf("the pause wrote customer rows: %v -> %v", before, after)
			}

			// A second tick changes nothing: the pause is a state, not an
			// event that has to be caught once.
			p.r.Tick(ctx)
			if got := p.served(t); !equal(got, want) {
				t.Fatalf("a second tick while paused: %v, want %v", got, want)
			}

			p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Updates(reason.resume)
			p.r.Tick(ctx)
			if got := p.served(t); !equal(got, ownSwitch) {
				t.Fatalf("after resuming: %v, want each customer's own switch %v", got, ownSwitch)
			}
		})
	}
}

// Resuming for one reason is not resuming: a reseller switched off whose
// term also ended stays paused when only one of the two is lifted.
func TestLiftingOneReasonOfTwoLeavesThePause(t *testing.T) {
	p := newPauseRig(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Minute)

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).
		Updates(map[string]any{"enabled": false, "expires_at": past})
	p.r.Tick(ctx)

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Update("enabled", true)
	p.r.Tick(ctx)
	if p.served(t)["active"] {
		t.Fatal("switching a reseller back on served their customers while their term was still over")
	}

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).
		Update("expires_at", time.Now().UTC().Add(time.Hour))
	p.r.Tick(ctx)
	if !p.served(t)["active"] {
		t.Fatal("lifting the last reason did not bring the customers back")
	}
}

// What the owner does to a customer while their reseller is paused is kept:
// switched on, they come back with the reseller; switched off, they do not.
func TestTheOwnersChangesDuringAPauseAreKept(t *testing.T) {
	p := newPauseRig(t)
	ctx := context.Background()

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Update("enabled", false)
	p.r.Tick(ctx)

	p.db.Model(&model.Client{}).Where("id = ?", p.ids["active"]).Update("status", model.StatusDisabled)
	p.db.Model(&model.Client{}).Where("id = ?", p.ids["disabled"]).Update("status", model.StatusActive)
	p.r.Tick(ctx)
	if got := p.served(t); got["active"] || got["disabled"] {
		t.Fatalf("a change to a customer let them past their reseller's pause: %v", got)
	}

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Update("enabled", true)
	p.r.Tick(ctx)
	got := p.served(t)
	if got["active"] {
		t.Error("a customer switched off during the pause came back with the reseller")
	}
	if !got["disabled"] {
		t.Error("a customer switched on during the pause did not come back with the reseller")
	}
}

// A reseller removed with their customers kept hands them to the owner, and
// they are served again, whatever the reseller's standing was.
func TestAResellerRemovedHandsTheirCustomersBack(t *testing.T) {
	p := newPauseRig(t)
	ctx := context.Background()

	p.db.Model(&model.Admin{}).Where("id = ?", p.paused.ID).Update("enabled", false)
	p.r.Tick(ctx)

	p.db.Model(&model.Client{}).Where("owner_id = ?", p.paused.ID).Update("owner_id", 0)
	p.db.Delete(&model.Admin{}, p.paused.ID)
	p.r.Tick(ctx)
	if got := p.served(t); !equal(got, ownSwitch) {
		t.Fatalf("customers handed back to the owner: %v, want their own switches %v", got, ownSwitch)
	}
}

func equal(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func equalStatus(a, b map[string]model.ClientStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
