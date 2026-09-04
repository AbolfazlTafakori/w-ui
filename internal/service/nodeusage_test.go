package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

// A host's allowance starts again on a day of the month, and the counter has to
// follow it. Getting this wrong in either direction costs real money: a reset
// that fires too often never stops anybody, and one that never fires stops
// everybody a month later for traffic they did not use.
func TestTheMonthRollsOverOnTheDayTheHostSays(t *testing.T) {
	cases := []struct {
		name string
		day  int
		last time.Time
		now  time.Time
		want bool
	}{
		{"the day has come round again", 1, at(2026, time.August, 3), at(2026, time.September, 1), true},
		{"and again the month after", 1, at(2026, time.September, 1), at(2026, time.October, 1), true},
		{"not before it arrives", 15, at(2026, time.September, 15), at(2026, time.September, 20), false},
		{"nor twice in the same month", 15, at(2026, time.September, 16), at(2026, time.September, 30), false},
		{"a late cleared counter still waits for the next one", 15, at(2026, time.September, 20), at(2026, time.October, 14), false},
		{"and rolls over when it gets there", 15, at(2026, time.September, 20), at(2026, time.October, 15), true},
		// The gap that matters: a panel down for six weeks must roll over on
		// the tick it comes back, not stay on a month that ended in the dark.
		{"a panel that was off for weeks rolls over as soon as it is back", 5,
			at(2026, time.July, 6), at(2026, time.September, 9), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			last := tc.last
			if got := resetDue(tc.day, &last, tc.now); got != tc.want {
				t.Errorf("resetDue(day %d, last %s, now %s) = %v, want %v",
					tc.day, tc.last.Format("2006-01-02"), tc.now.Format("2006-01-02"),
					got, tc.want)
			}
		})
	}
}

// Day zero is a machine on an unmetered line: a counter only an operator
// clears. It must never roll over on its own, or an operator watching a total
// would find it back at nothing for no reason they could see.
func TestAnUnmeteredServerNeverRollsOver(t *testing.T) {
	last := at(2020, time.January, 1)
	if resetDue(0, &last, at(2026, time.September, 4)) {
		t.Error("a server with no reset day rolled its counter over")
	}
}

// A counter that has never been cleared establishes its boundary on the first
// tick. Without this the first month would be measured from whenever the row
// happened to be created, which is not a month.
func TestTheFirstTickEstablishesTheBoundary(t *testing.T) {
	if !resetDue(10, nil, at(2026, time.September, 4)) {
		t.Error("a counter that has never been cleared did not start its first period")
	}
}

// ── what the database ends up holding ────────────────────────────────────────

func TestTrafficAddsUpAcrossTicks(t *testing.T) {
	db := testDB(t)
	node := model.Node{Name: "berlin", Kind: model.KindRemote, DataLimitBytes: 10 << 30}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	now := at(2026, time.September, 4)
	for i := 0; i < 3; i++ {
		if err := RecordNodeTraffic(context.Background(), db, node.ID, 1<<30, now); err != nil {
			t.Fatalf("RecordNodeTraffic: %v", err)
		}
	}

	var got model.Node
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.UsedBytes != 3<<30 {
		t.Errorf("the node counted %d bytes, want %d", got.UsedBytes, uint64(3)<<30)
	}
	if got.OverAllowance {
		t.Error("3 GB of a 10 GB allowance was called spent")
	}
}

// Reaching the allowance is what takes customers off the server, so the flag
// has to turn over exactly at the limit rather than past it.
func TestReachingTheAllowanceIsSpent(t *testing.T) {
	db := testDB(t)
	node := model.Node{Name: "berlin", Kind: model.KindRemote, DataLimitBytes: 1 << 30}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	if err := RecordNodeTraffic(context.Background(), db, node.ID, 1<<30, at(2026, time.September, 4)); err != nil {
		t.Fatalf("RecordNodeTraffic: %v", err)
	}

	var got model.Node
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.OverAllowance {
		t.Errorf("a node that used its whole %d byte allowance is not marked spent", got.DataLimitBytes)
	}
}

// A server with no limit is never spent, whatever it carries. This is the
// default and the case every existing installation is in.
func TestAServerWithNoLimitIsNeverSpent(t *testing.T) {
	db := testDB(t)
	node := model.Node{Name: "berlin", Kind: model.KindRemote}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	if err := RecordNodeTraffic(context.Background(), db, node.ID, 900<<30, at(2026, time.September, 4)); err != nil {
		t.Fatalf("RecordNodeTraffic: %v", err)
	}

	var got model.Node
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.OverAllowance {
		t.Error("a server with no transfer limit was cut off")
	}
}

// The rollover keeps the bytes that arrived after the boundary. Discarding the
// tick that happens to straddle it would lose up to a whole interval of traffic
// every month, silently and always in the customer's favour.
func TestTheRolloverKeepsTheTrafficFromThatTick(t *testing.T) {
	db := testDB(t)
	last := at(2026, time.August, 20)
	node := model.Node{
		Name: "berlin", Kind: model.KindRemote,
		DataLimitBytes: 10 << 30, UsedBytes: 9 << 30,
		ResetDay: 1, UsageResetAt: &last,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	if err := RecordNodeTraffic(context.Background(), db, node.ID, 512<<20, at(2026, time.September, 1)); err != nil {
		t.Fatalf("RecordNodeTraffic: %v", err)
	}

	var got model.Node
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.UsedBytes != 512<<20 {
		t.Errorf("after the rollover the node holds %d bytes, want %d",
			got.UsedBytes, uint64(512)<<20)
	}
	if got.UsageResetAt == nil || !got.UsageResetAt.Equal(at(2026, time.September, 1)) {
		t.Errorf("the rollover did not record when it happened: %v", got.UsageResetAt)
	}
	if got.OverAllowance {
		t.Error("a node that has just started a new month is still cut off")
	}
}
