package service

import (
	"context"
	"math"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

const gib = uint64(1) << 30

// extendDB is a panel with the scope registered and no customers yet.
func extendDB(t *testing.T) (*gorm.DB, *Clients, *Admins) {
	t.Helper()
	return resellerDB(t)
}

// customer writes one customer row as it is, with the fields a test cares
// about; the status is whatever the test says it is.
func customer(t *testing.T, db *gorm.DB, c model.Client) *model.Client {
	t.Helper()
	if c.SubToken == "" {
		c.SubToken = "tok-" + c.Name
	}
	if c.Status == "" {
		c.Status = model.StatusActive
	}
	status := c.Status
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create %s: %v", c.Name, err)
	}
	// Written after: a default on the column must not stand in for it.
	if err := db.Model(&model.Client{}).Where("id = ?", c.ID).Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
	c.Status = status
	return &c
}

func reload(t *testing.T, db *gorm.DB, id uint) model.Client {
	t.Helper()
	var c model.Client
	if err := db.First(&c, id).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func fromNow(d time.Duration) *time.Time {
	v := time.Now().UTC().Add(d).Truncate(time.Second)
	return &v
}

const day = 24 * time.Hour

func near(a, b time.Time) bool { return a.Sub(b).Abs() < 2*time.Second }

// Time moves each customer's own end date -- never "from today" -- and each
// customer ends up in the state their new date puts them in.
func TestTimeMovesEachCustomersOwnEndDate(t *testing.T) {
	db, clients, _ := extendDB(t)
	running := customer(t, db, model.Client{Name: "running", ExpiresAt: fromNow(10 * day)})
	longAgo := customer(t, db, model.Client{Name: "long-ago", ExpiresAt: fromNow(-5 * day), Status: model.StatusExpired})
	yesterday := customer(t, db, model.Client{Name: "yesterday", ExpiresAt: fromNow(-1 * day), Status: model.StatusExpired})
	unlimited := customer(t, db, model.Client{Name: "unlimited"})
	off := customer(t, db, model.Client{Name: "off", ExpiresAt: fromNow(5 * day), Status: model.StatusDisabled})
	waiting := customer(t, db, model.Client{Name: "waiting", StartOnFirstUse: true, DurationDays: 30})

	ids := []uint{running.ID, longAgo.ID, yesterday.ID, unlimited.ID, off.ID, waiting.ID}
	res, err := clients.Extend(context.Background(), ExtendInput{IDs: ids, Kind: ExtendTime, Days: 2})
	if err != nil {
		t.Fatal(err)
	}

	if got := reload(t, db, running.ID); !near(*got.ExpiresAt, running.ExpiresAt.Add(2*day)) || got.Status != model.StatusActive {
		t.Errorf("a running plan: %v %s, want two days later and active", got.ExpiresAt, got.Status)
	}
	// Ended five days ago, given two: ends three days ago, still over.
	if got := reload(t, db, longAgo.ID); !near(*got.ExpiresAt, longAgo.ExpiresAt.Add(2*day)) || got.Status != model.StatusExpired {
		t.Errorf("ended five days ago: %v %s, want three days ago and still expired", got.ExpiresAt, got.Status)
	}
	// Ended a day ago, given two: the lost day is gone, one day runs.
	if got := reload(t, db, yesterday.ID); !near(*got.ExpiresAt, yesterday.ExpiresAt.Add(2*day)) || got.Status != model.StatusActive {
		t.Errorf("ended a day ago: %v %s, want a day from now and active again", got.ExpiresAt, got.Status)
	}
	if got := reload(t, db, unlimited.ID); got.ExpiresAt != nil {
		t.Errorf("a customer with no end date was given one: %v", got.ExpiresAt)
	}
	if got := reload(t, db, off.ID); !near(*got.ExpiresAt, off.ExpiresAt.Add(2*day)) || got.Status != model.StatusDisabled {
		t.Errorf("switched off: %v %s, want the time and still off", got.ExpiresAt, got.Status)
	}
	if got := reload(t, db, waiting.ID); got.DurationDays != 30 || got.ExpiresAt != nil {
		t.Errorf("a plan not started was changed: %d days, %v", got.DurationDays, got.ExpiresAt)
	}

	if res.Changed != 4 || res.Skipped[SkipUnlimited] != 1 || res.Skipped[SkipWaiting] != 1 ||
		res.Revived != 1 || res.StillEnded != 1 || res.Ended != 0 {
		t.Errorf("result = %+v", res)
	}
	if res.Amount != int64(2*day/time.Second) {
		t.Errorf("amount = %d seconds, want two days", res.Amount)
	}
}

// A month is thirty days and a day twenty-four hours, exactly.
func TestMonthsDaysAndHoursAreExact(t *testing.T) {
	db, clients, _ := extendDB(t)
	c := customer(t, db, model.Client{Name: "c", ExpiresAt: fromNow(day)})
	if _, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{c.ID}, Kind: ExtendTime, Months: 1, Days: 2, Hours: 3,
	}); err != nil {
		t.Fatal(err)
	}
	want := c.ExpiresAt.Add((30*24 + 2*24 + 3) * time.Hour)
	if got := reload(t, db, c.ID); !near(*got.ExpiresAt, want) {
		t.Errorf("1 month 2 days 3 hours moved the date to %v, want %v", got.ExpiresAt, want)
	}

	// Half a day is twelve hours.
	if _, err := clients.Extend(context.Background(), ExtendInput{IDs: []uint{c.ID}, Kind: ExtendTime, Days: 0.5}); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, c.ID); !near(*got.ExpiresAt, want.Add(12*time.Hour)) {
		t.Errorf("half a day moved the date to %v, want twelve hours more", got.ExpiresAt)
	}
}

// Asked for, a plan that has not started gets the time on its length, to the
// hour, and still starts on first connection.
func TestAWaitingPlanGetsTimeOnlyWhenAskedAndToTheHour(t *testing.T) {
	db, clients, _ := extendDB(t)
	w := customer(t, db, model.Client{Name: "w", StartOnFirstUse: true, DurationDays: 30})
	res, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{w.ID}, Kind: ExtendTime, Days: 1, Hours: 12, IncludeWaiting: true,
	})
	if err != nil || res.Changed != 1 {
		t.Fatalf("Extend = %+v, %v", res, err)
	}
	got := reload(t, db, w.ID)
	if got.DurationDays != 31 || got.DurationHours != 12 || got.ExpiresAt != nil || !got.StartOnFirstUse {
		t.Errorf("waiting plan = %d days %d hours, expires %v, first use %v; want 31 days 12 hours, still waiting",
			got.DurationDays, got.DurationHours, got.ExpiresAt, got.StartOnFirstUse)
	}

	// Twelve hours more carries into a whole day.
	if _, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{w.ID}, Kind: ExtendTime, Hours: 12, IncludeWaiting: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, w.ID); got.DurationDays != 32 || got.DurationHours != 0 {
		t.Errorf("after twelve more hours: %d days %d hours, want 32 days", got.DurationDays, got.DurationHours)
	}

	// Taking back more than the plan has leaves it as it was, and says why.
	res, err = clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{w.ID}, Kind: ExtendTime, Months: 2, Subtract: true, IncludeWaiting: true,
	})
	if err != nil || res.Skipped[SkipTooShort] != 1 || res.Changed != 0 {
		t.Fatalf("taking back two months from 32 days = %+v, %v", res, err)
	}
	if got := reload(t, db, w.ID); got.DurationDays != 32 {
		t.Errorf("a refused take-back changed the plan to %d days", got.DurationDays)
	}
}

// Taking time back is allowed, for a mistake; past the end date it ends the
// plan.
func TestTakingTimeBackCanEndAPlan(t *testing.T) {
	db, clients, _ := extendDB(t)
	c := customer(t, db, model.Client{Name: "c", ExpiresAt: fromNow(day)})
	res, err := clients.Extend(context.Background(), ExtendInput{IDs: []uint{c.ID}, Kind: ExtendTime, Days: 2, Subtract: true})
	if err != nil {
		t.Fatal(err)
	}
	got := reload(t, db, c.ID)
	if !near(*got.ExpiresAt, c.ExpiresAt.Add(-2*day)) || got.Status != model.StatusExpired || res.Ended != 1 {
		t.Errorf("two days back from one left: %v %s, result %+v; want expired a day ago", got.ExpiresAt, got.Status, res)
	}
	if res.Amount != -int64(2*day/time.Second) {
		t.Errorf("amount = %d, want minus two days", res.Amount)
	}
}

// Traffic moves the allowance, never the usage; a customer without a limit
// keeps having none; one out of traffic runs again when there is more.
func TestTrafficMovesTheAllowanceNotTheUsage(t *testing.T) {
	db, clients, _ := extendDB(t)
	half := customer(t, db, model.Client{Name: "half", QuotaBytes: 20 * gib, UsedBytes: 10 * gib})
	out := customer(t, db, model.Client{Name: "out", QuotaBytes: 10 * gib, UsedBytes: 10 * gib, Status: model.StatusExhausted})
	unlimited := customer(t, db, model.Client{Name: "unlimited", UsedBytes: 99 * gib})
	ended := customer(t, db, model.Client{Name: "ended", QuotaBytes: 10 * gib, ExpiresAt: fromNow(-day), Status: model.StatusExpired})
	off := customer(t, db, model.Client{Name: "off", QuotaBytes: 10 * gib, Status: model.StatusDisabled})

	res, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{half.ID, out.ID, unlimited.ID, ended.ID, off.ID}, Kind: ExtendTraffic, GB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, half.ID); got.QuotaBytes != 25*gib || got.UsedBytes != 10*gib {
		t.Errorf("half used: %d of %d, want 10 GiB of 25", got.UsedBytes, got.QuotaBytes)
	}
	if got := reload(t, db, out.ID); got.QuotaBytes != 15*gib || got.Status != model.StatusActive {
		t.Errorf("out of traffic: %d, %s; want 15 GiB and running again", got.QuotaBytes, got.Status)
	}
	if got := reload(t, db, unlimited.ID); got.QuotaBytes != 0 {
		t.Errorf("an unlimited allowance was capped at %d", got.QuotaBytes)
	}
	if got := reload(t, db, ended.ID); got.QuotaBytes != 15*gib || got.Status != model.StatusExpired {
		t.Errorf("ended by date: %d, %s; want the traffic and still expired", got.QuotaBytes, got.Status)
	}
	if got := reload(t, db, off.ID); got.QuotaBytes != 15*gib || got.Status != model.StatusDisabled {
		t.Errorf("switched off: %d, %s; want the traffic and still off", got.QuotaBytes, got.Status)
	}
	if res.Changed != 4 || res.Skipped[SkipUnlimited] != 1 || res.Revived != 1 {
		t.Errorf("result = %+v", res)
	}
}

// TB, GB and MB count in 1024s, as the panel shows sizes.
func TestTrafficUnitsAreTheOnesThePanelShows(t *testing.T) {
	db, clients, _ := extendDB(t)
	c := customer(t, db, model.Client{Name: "c", QuotaBytes: gib})
	if _, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{c.ID}, Kind: ExtendTraffic, TB: 1, GB: 2, MB: 512,
	}); err != nil {
		t.Fatal(err)
	}
	want := gib + 1<<40 + 2*gib + 512<<20
	if got := reload(t, db, c.ID); got.QuotaBytes != want {
		t.Errorf("1 TB 2 GB 512 MB made %d, want %d", got.QuotaBytes, want)
	}
}

// Taking traffic back stops at what the customer has used, and never at
// nothing, which would read as no limit.
func TestTakingTrafficBackStopsAtUsage(t *testing.T) {
	db, clients, _ := extendDB(t)
	busy := customer(t, db, model.Client{Name: "busy", QuotaBytes: 20 * gib, UsedBytes: 15 * gib})
	fresh := customer(t, db, model.Client{Name: "fresh", QuotaBytes: 5 * gib})

	res, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{busy.ID, fresh.ID}, Kind: ExtendTraffic, GB: 10, Subtract: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, busy.ID); got.QuotaBytes != 15*gib || got.Status != model.StatusExhausted {
		t.Errorf("10 GiB back from 20 with 15 used: %d, %s; want 15 GiB and out of traffic", got.QuotaBytes, got.Status)
	}
	if got := reload(t, db, fresh.ID); got.QuotaBytes == 0 || got.Status != model.StatusExhausted {
		t.Errorf("more taken back than an unused allowance: %d, %s; want a limit, not none, and out of traffic",
			got.QuotaBytes, got.Status)
	}
	if res.Floored != 2 || res.Ended != 2 {
		t.Errorf("result = %+v, want both floored and both ended", res)
	}
}

// A dry run says what would happen and changes nothing.
func TestADryRunChangesNothing(t *testing.T) {
	db, clients, _ := extendDB(t)
	c := customer(t, db, model.Client{Name: "c", ExpiresAt: fromNow(-day), QuotaBytes: gib, Status: model.StatusExpired})
	u := customer(t, db, model.Client{Name: "u"})
	res, err := clients.Extend(context.Background(), ExtendInput{
		IDs: []uint{c.ID, u.ID}, Kind: ExtendTime, Days: 3, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || res.Skipped[SkipUnlimited] != 1 || res.Revived != 1 {
		t.Errorf("dry run said %+v", res)
	}
	got := reload(t, db, c.ID)
	if !near(*got.ExpiresAt, *c.ExpiresAt) || got.Status != model.StatusExpired {
		t.Errorf("a dry run changed the customer: %v %s", got.ExpiresAt, got.Status)
	}
}

// Nothing selected changes nothing, and amounts that are not amounts are
// refused before anything is read.
func TestWhatIsRefused(t *testing.T) {
	_, clients, _ := extendDB(t)
	for name, in := range map[string]ExtendInput{
		"nothing selected":  {Kind: ExtendTime, Days: 1},
		"no kind":           {IDs: []uint{1}, Days: 1},
		"no time":           {IDs: []uint{1}, Kind: ExtendTime},
		"no traffic":        {IDs: []uint{1}, Kind: ExtendTraffic},
		"a negative number": {IDs: []uint{1}, Kind: ExtendTime, Days: -3},
		"not a number":      {IDs: []uint{1}, Kind: ExtendTraffic, GB: math.NaN()},
		"endless":           {IDs: []uint{1}, Kind: ExtendTime, Days: math.Inf(1)},
		"a thousand years":  {IDs: []uint{1}, Kind: ExtendTime, Months: 12000},
		"more than a PiB":   {IDs: []uint{1}, Kind: ExtendTraffic, TB: 2048},
		"time for traffic":  {IDs: []uint{1}, Kind: ExtendTraffic, Days: 3},
		"traffic for time":  {IDs: []uint{1}, Kind: ExtendTime, GB: 3},
	} {
		if _, err := clients.Extend(context.Background(), in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A reseller's selection reaches their own customers alone, whatever ids were
// sent; the others are counted as not found and left as they are.
func TestAResellerReachesOnlyTheirOwn(t *testing.T) {
	db, clients, admins := extendDB(t)
	reza := newReseller(t, admins, "reza", 0)
	own := customer(t, db, model.Client{Name: "rezas", OwnerID: reza.ID, ExpiresAt: fromNow(day)})
	owners := customer(t, db, model.Client{Name: "owners", ExpiresAt: fromNow(day)})

	res, err := clients.Extend(asReseller(reza, 1), ExtendInput{
		IDs: []uint{own.ID, owners.ID}, Kind: ExtendTime, Days: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || res.Skipped[SkipGone] != 1 {
		t.Errorf("result = %+v", res)
	}
	if got := reload(t, db, owners.ID); !near(*got.ExpiresAt, *owners.ExpiresAt) {
		t.Error("a reseller changed a customer who is not theirs")
	}
	if got := reload(t, db, own.ID); !near(*got.ExpiresAt, own.ExpiresAt.Add(5*day)) {
		t.Error("a reseller's own customer was not given the time")
	}
}

// "Select all" is every customer the filter matches, across every page -- and
// a reseller's is their own.
func TestMatchingIDsIsEveryPage(t *testing.T) {
	db, clients, admins := extendDB(t)
	for i := range 60 {
		name := "vip-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		customer(t, db, model.Client{Name: name, SubToken: "t-" + name})
	}
	customer(t, db, model.Client{Name: "other", SubToken: "t-other"})

	ids, err := clients.MatchingIDs(context.Background(), ListFilter{Search: "vip", PerPage: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 60 {
		t.Errorf("select all matched %d, want all 60 across the pages", len(ids))
	}

	reza := newReseller(t, admins, "reza", 0)
	customer(t, db, model.Client{Name: "vip-rezas", SubToken: "t-rezas", OwnerID: reza.ID})
	ids, err = clients.MatchingIDs(asReseller(reza, 1), ListFilter{Search: "vip"})
	if err != nil || len(ids) != 1 {
		t.Errorf("a reseller's select all = %v, %v; want their one customer", ids, err)
	}
}
