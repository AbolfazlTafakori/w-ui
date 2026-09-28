package service

import (
	"sort"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"gorm.io/gorm"
)

// bulkRig is the owner, a panel administrator and three resellers, each in a
// different state: one running a dated term, one on hold, one with no end,
// and one already switched off.
type bulkRig struct {
	db      *gorm.DB
	clients *Clients
	admins  *Admins
	owner   *model.Admin
	admin   *model.Admin
	dated   *model.Admin
	onHold  *model.Admin
	noEnd   *model.Admin
	off     *model.Admin
}

func newBulkRig(t *testing.T) *bulkRig {
	t.Helper()
	db, clients, admins := resellerDB(t)
	r := &bulkRig{db: db, clients: clients, admins: admins}

	r.owner = &model.Admin{Username: "owner", PasswordHash: "x", Role: model.RoleOwner, Enabled: true}
	if err := db.Create(r.owner).Error; err != nil {
		t.Fatal(err)
	}
	mk := func(name string, role model.AdminRole, in AdminInput) *model.Admin {
		in.Username, in.Password, in.Role = name, "correct-horse", role
		if role == model.RoleReseller && in.InterfaceIDs == nil {
			in.InterfaceIDs = []uint{1}
		}
		a, err := admins.Create(t.Context(), in)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return a
	}
	in30 := time.Now().UTC().Add(30 * 24 * time.Hour)
	ten, off := 10, false
	r.admin = mk("helper", model.RoleAdmin, AdminInput{})
	r.dated = mk("dated", model.RoleReseller, AdminInput{ExpiresAt: OptionalTime{Set: true, Value: &in30}})
	r.onHold = mk("on-hold", model.RoleReseller, AdminInput{DurationDays: &ten})
	r.noEnd = mk("no-end", model.RoleReseller, AdminInput{})
	r.off = mk("switched-off", model.RoleReseller, AdminInput{Enabled: &off})
	return r
}

func (r *bulkRig) reload(t *testing.T, a *model.Admin) model.Admin {
	t.Helper()
	got, err := r.admins.Get(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return *got
}

func (r *bulkRig) run(t *testing.T, in AdminBulkInput) *AdminBulkResult {
	t.Helper()
	res, err := r.admins.Bulk(t.Context(), in, r.clients)
	if err != nil {
		t.Fatalf("%s: %v", in.Action, err)
	}
	return res
}

func adminIDs(as ...*model.Admin) []uint {
	out := make([]uint, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func TestBulkSwitchingResellersOffAndOn(t *testing.T) {
	r := newBulkRig(t)
	before := r.reload(t, r.dated).SessionEpoch

	res := r.run(t, AdminBulkInput{Action: AdminBulkDisable, IDs: adminIDs(r.dated, r.noEnd, r.off)})
	if res.Changed != 2 || res.Unchanged != 1 || len(res.Failures) != 0 {
		t.Fatalf("switching off two on and one already off: %+v", res)
	}
	for _, a := range []*model.Admin{r.dated, r.noEnd} {
		if r.reload(t, a).Enabled {
			t.Errorf("%s is still on", a.Username)
		}
	}
	// Through Update, so switched off means signed out.
	if after := r.reload(t, r.dated).SessionEpoch; after == before {
		t.Error("switching off in bulk left the reseller's sessions open")
	}

	res = r.run(t, AdminBulkInput{Action: AdminBulkEnable, IDs: adminIDs(r.dated, r.noEnd, r.off, r.onHold)})
	if res.Changed != 3 || res.Unchanged != 1 {
		t.Fatalf("switching three off and one on back on: %+v", res)
	}
}

func TestBulkRefusesTheOwnerAndChangesTheRest(t *testing.T) {
	r := newBulkRig(t)
	res := r.run(t, AdminBulkInput{Action: AdminBulkDisable, IDs: adminIDs(r.owner, r.dated)})
	if res.Changed != 1 || len(res.Failures) != 1 || res.Failures["owner"] == "" {
		t.Fatalf("the owner in a selection: %+v", res)
	}
	if !r.reload(t, r.owner).Enabled {
		t.Fatal("the owner was switched off from a selection")
	}
	if r.reload(t, r.dated).Enabled {
		t.Fatal("the reseller beside the owner was not switched off")
	}
}

func TestBulkCeilingActionsSkipAPanelAdministrator(t *testing.T) {
	r := newBulkRig(t)
	gb := uint64(100) << 30
	five := 5
	for _, in := range []AdminBulkInput{
		{Action: AdminBulkSetQuota, QuotaBytes: &gb},
		{Action: AdminBulkSetLimit, ClientLimit: &five},
		{Action: AdminBulkExtend, Days: 30},
		{Action: AdminBulkResetUsage},
		{Action: AdminBulkAddServers, InterfaceIDs: []uint{2}},
	} {
		in.IDs = adminIDs(r.admin)
		res := r.run(t, in)
		if res.Changed != 0 || res.Failures["helper"] == "" {
			t.Errorf("%s on a panel administrator: %+v", in.Action, res)
		}
	}
}

func TestBulkExtendingEveryKindOfTerm(t *testing.T) {
	r := newBulkRig(t)
	past := time.Now().UTC().Add(-48 * time.Hour)
	expired, err := r.admins.Create(t.Context(), AdminInput{Username: "expired", Password: "correct-horse",
		Role: model.RoleReseller, InterfaceIDs: []uint{1}, ExpiresAt: OptionalTime{Set: true, Value: &past}})
	if err != nil {
		t.Fatal(err)
	}
	datedBefore := *r.reload(t, r.dated).ExpiresAt

	res := r.run(t, AdminBulkInput{Action: AdminBulkExtend, Days: 30, IDs: adminIDs(r.dated, r.onHold, r.noEnd, expired)})
	if res.Changed != 3 || res.Unchanged != 1 {
		t.Fatalf("extending four: %+v", res)
	}

	// A running term moves from where it was.
	if got := r.reload(t, r.dated).ExpiresAt; got == nil || got.Sub(datedBefore).Round(time.Hour) != 30*24*time.Hour {
		t.Errorf("a running term moved to %v from %v, want 30 days on", got, datedBefore)
	}
	// An ended one starts again from now, not from when it ended.
	if got := r.reload(t, expired).ExpiresAt; got == nil || time.Until(*got).Round(time.Hour) != 30*24*time.Hour {
		t.Errorf("an ended term moved to %v, want 30 days from now", got)
	}
	// A term on hold gets the days added to the hold.
	if got := r.reload(t, r.onHold); got.DurationDays != 40 || got.ExpiresAt != nil {
		t.Errorf("a term on hold: days=%d expires=%v, want 40 days still on hold", got.DurationDays, got.ExpiresAt)
	}
	// No end is left with no end.
	if got := r.reload(t, r.noEnd).ExpiresAt; got != nil {
		t.Errorf("a reseller with no end date was given one: %v", got)
	}
}

func TestBulkSettingCeilings(t *testing.T) {
	r := newBulkRig(t)
	gb := uint64(500) << 30
	res := r.run(t, AdminBulkInput{Action: AdminBulkSetQuota, QuotaBytes: &gb, IDs: adminIDs(r.dated, r.noEnd)})
	if res.Changed != 2 {
		t.Fatalf("setting a quota: %+v", res)
	}
	if r.reload(t, r.dated).QuotaBytes != gb {
		t.Error("the quota was not set")
	}
	// Setting it again is no change.
	if res := r.run(t, AdminBulkInput{Action: AdminBulkSetQuota, QuotaBytes: &gb, IDs: adminIDs(r.dated)}); res.Unchanged != 1 {
		t.Errorf("setting the same quota again: %+v", res)
	}

	fifty := 50
	res = r.run(t, AdminBulkInput{Action: AdminBulkSetLimit, ClientLimit: &fifty, IDs: adminIDs(r.dated, r.onHold)})
	if res.Changed != 2 || r.reload(t, r.onHold).ClientLimit != 50 {
		t.Fatalf("setting a customer limit: %+v", res)
	}
}

func TestBulkResettingAllowances(t *testing.T) {
	r := newBulkRig(t)
	r.db.Model(&model.Admin{}).Where("id = ?", r.dated.ID).Update("used_bytes", 12345)
	res := r.run(t, AdminBulkInput{Action: AdminBulkResetUsage, IDs: adminIDs(r.dated, r.noEnd)})
	if res.Changed != 1 || res.Unchanged != 1 {
		t.Fatalf("resetting one used and one untouched: %+v", res)
	}
	if r.reload(t, r.dated).UsedBytes != 0 {
		t.Error("the allowance was not started again")
	}
}

func TestBulkAddingAndRemovingServers(t *testing.T) {
	r := newBulkRig(t)
	servers := func(a *model.Admin) []uint {
		got := r.reload(t, a).InterfaceIDs
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		return got
	}

	res := r.run(t, AdminBulkInput{Action: AdminBulkAddServers, InterfaceIDs: []uint{2}, IDs: adminIDs(r.dated, r.noEnd)})
	if res.Changed != 2 || len(servers(r.dated)) != 2 {
		t.Fatalf("adding a server: %+v, now %v", res, servers(r.dated))
	}
	// Adding one they already have is no change.
	if res := r.run(t, AdminBulkInput{Action: AdminBulkAddServers, InterfaceIDs: []uint{2}, IDs: adminIDs(r.dated)}); res.Unchanged != 1 {
		t.Errorf("adding a server they already have: %+v", res)
	}

	// Taking every server away leaves a reseller with none -- a real state,
	// not "leave it alone".
	res = r.run(t, AdminBulkInput{Action: AdminBulkRemoveServers, InterfaceIDs: []uint{1, 2}, IDs: adminIDs(r.dated)})
	if res.Changed != 1 || len(servers(r.dated)) != 0 {
		t.Fatalf("removing every server: %+v, now %v", res, servers(r.dated))
	}

	// A server that does not exist is refused for that reseller.
	res = r.run(t, AdminBulkInput{Action: AdminBulkAddServers, InterfaceIDs: []uint{999}, IDs: adminIDs(r.noEnd)})
	if res.Changed != 0 || res.Failures["no-end"] == "" {
		t.Errorf("adding a server that does not exist: %+v", res)
	}
}

func TestBulkDeletingResellers(t *testing.T) {
	for _, mode := range []DeleteMode{DeleteKeepClients, DeleteWithClients} {
		t.Run(string(mode), func(t *testing.T) {
			r := newBulkRig(t)
			c, err := r.clients.Create(asReseller(r.dated, 1), CreateInput{Name: "theirs", InterfaceIDs: []uint{1}})
			if err != nil {
				t.Fatal(err)
			}
			res := r.run(t, AdminBulkInput{Action: AdminBulkDelete, Mode: mode, IDs: adminIDs(r.dated, r.noEnd)})
			if res.Changed != 2 {
				t.Fatalf("deleting two: %+v", res)
			}
			var left int64
			r.db.Model(&model.Admin{}).Where("id IN ?", adminIDs(r.dated, r.noEnd)).Count(&left)
			if left != 0 {
				t.Fatal("the resellers are still there")
			}
			var kept model.Client
			found := r.db.First(&kept, c.ID).Error == nil
			if mode == DeleteKeepClients && (!found || kept.OwnerID != 0) {
				t.Errorf("keep: the customer is found=%v owner=%d, want kept under the owner", found, kept.OwnerID)
			}
			if mode == DeleteWithClients && found {
				t.Error("delete: the customer is still there")
			}
		})
	}
}

func TestBulkRefusesWhatItCannotDo(t *testing.T) {
	r := newBulkRig(t)
	for name, in := range map[string]AdminBulkInput{
		"nothing selected":        {Action: AdminBulkEnable},
		"an unknown action":       {Action: "promote", IDs: adminIDs(r.dated)},
		"extend by no days":       {Action: AdminBulkExtend, IDs: adminIDs(r.dated)},
		"extend past the limit":   {Action: AdminBulkExtend, Days: maxTermDays + 1, IDs: adminIDs(r.dated)},
		"a quota not given":       {Action: AdminBulkSetQuota, IDs: adminIDs(r.dated)},
		"a negative limit":        {Action: AdminBulkSetLimit, ClientLimit: new(int), IDs: adminIDs(r.dated)},
		"servers not given":       {Action: AdminBulkAddServers, IDs: adminIDs(r.dated)},
		"delete without a choice": {Action: AdminBulkDelete, IDs: adminIDs(r.dated)},
	} {
		if name == "a negative limit" {
			*in.ClientLimit = -1
		}
		if _, err := r.admins.Bulk(t.Context(), in, r.clients); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Nothing was touched by any of it.
	if !r.reload(t, r.dated).Enabled {
		t.Fatal("a refused request changed a reseller")
	}
}

// Selected ids that no longer exist are counted, not failed: a row deleted
// in another tab is not an error about any reseller.
func TestBulkCountsIdsThatAreGone(t *testing.T) {
	r := newBulkRig(t)
	res := r.run(t, AdminBulkInput{Action: AdminBulkDisable, IDs: []uint{r.dated.ID, 99999}})
	if res.Changed != 1 || res.Unchanged != 1 || len(res.Failures) != 0 {
		t.Fatalf("a selection with a missing id: %+v", res)
	}
}
