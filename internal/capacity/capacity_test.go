package capacity

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A database on disk, as a panel runs one: several connections, writers
// waiting on each other the way two requests at once do.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	// The panel's own settings (internal/database): WAL, writers queued by
	// an immediate transaction lock and a busy timeout, eight connections.
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cap.db")+
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(15000)&_txlock=immediate"),
		&gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(8)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&model.CapacityAccount{}, &model.CapacityEntry{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func account(t *testing.T, db *gorm.DB, admin uint, mode string, granted int64) *model.CapacityAccount {
	t.Helper()
	a, err := Open(db, admin, model.CapacityUsers, mode)
	if err != nil {
		t.Fatal(err)
	}
	if granted > 0 {
		if err := Charge(db, a, granted, Entry{Reason: model.CapacityCharge}); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func reread(t *testing.T, db *gorm.DB, a *model.CapacityAccount) *model.CapacityAccount {
	t.Helper()
	got, err := Account(db, a.AdminID, a.Kind)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	return got
}

func balanced(t *testing.T, db *gorm.DB) {
	t.Helper()
	bad, err := Balanced(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("accounts whose figures are not the sum of their ledger: %v", bad)
	}
}

// Two hundred requests at once against fifty units: exactly fifty are let
// in, whatever order they run in, and the figure is fifty -- never more.
func TestRequestsAtOnceCannotPassTheCeiling(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityOpen, 50)
	var won atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.Transaction(func(tx *gorm.DB) error {
				acc, err := Account(tx, 7, model.CapacityUsers)
				if err != nil {
					return err
				}
				return Take(tx, acc, 1, Entry{Reason: model.CapacityCreate})
			})
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, ErrFull) {
				t.Errorf("a request failed otherwise: %v", err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 50 {
		t.Fatalf("%d requests were let in against 50 units", won.Load())
	}
	if got := reread(t, db, a); got.Used != 50 {
		t.Fatalf("used %d, want 50", got.Used)
	}
	balanced(t, db)
}

// Work that fails takes nothing: the unit goes back with the rollback.
func TestFailedWorkTakesNothing(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityConsumable, 10)
	boom := errors.New("the customer could not be made")
	err := db.Transaction(func(tx *gorm.DB) error {
		acc, _ := Account(tx, 7, model.CapacityUsers)
		if err := Take(tx, acc, 3, Entry{Reason: model.CapacityCreate}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if got := reread(t, db, a); got.Used != 0 {
		t.Fatalf("a failed create spent %d", got.Used)
	}
	balanced(t, db)
}

// Several at once are all or none: a batch that does not fit takes nothing.
func TestABatchIsAllOrNone(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityOpen, 5)
	err := db.Transaction(func(tx *gorm.DB) error {
		acc, _ := Account(tx, 7, model.CapacityUsers)
		for i := 0; i < 6; i++ {
			if err := Take(tx, acc, 1, Entry{Reason: model.CapacityCreate}); err != nil {
				return err
			}
		}
		return nil
	})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("six from five: %v", err)
	}
	if got := reread(t, db, a); got.Used != 0 {
		t.Fatalf("half a batch was kept: %d", got.Used)
	}
	balanced(t, db)
}

// What no plan could mean is refused before any arithmetic: a negative
// take, a negative charge, more than a million at once.
func TestNumbersNoPlanCouldMeanAreRefused(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityOpen, 10)
	for _, n := range []int64{-1, -1 << 62, MaxUnits + 1, 1 << 62} {
		if err := Take(db, a, n, Entry{}); err == nil {
			t.Errorf("take %d accepted", n)
		}
		if err := Charge(db, a, n, Entry{}); err == nil && n != MaxUnits+1 && n > 0 {
			t.Errorf("charge %d accepted", n)
		}
	}
	got := reread(t, db, a)
	if got.Used != 0 || got.Granted != 10 {
		t.Fatalf("figures moved: %+v", got)
	}
	balanced(t, db)
}

// A spent unit never comes back: a consumable account and a token account
// refuse to give anything back, whatever the caller asks.
func TestSpentUnitsNeverComeBack(t *testing.T) {
	db := testDB(t)
	b := account(t, db, 7, model.CapacityConsumable, 10)
	if err := Take(db, b, 4, Entry{Reason: model.CapacityCreate}); err != nil {
		t.Fatal(err)
	}
	if err := Give(db, b, 4, Entry{Reason: model.CapacityRelease}); err == nil {
		t.Fatal("a consumable account gave units back")
	}
	tok, _ := Open(db, 7, model.CapacityTokens, model.CapacityFixed)
	Charge(db, tok, 3, Entry{})
	Take(db, tok, 1, Entry{Reason: model.CapacityIssue})
	if err := Give(db, tok, 1, Entry{}); err == nil {
		t.Fatal("a token account gave a token back")
	}
	if got := reread(t, db, b); got.Used != 4 {
		t.Fatalf("consumable used %d", got.Used)
	}
	balanced(t, db)
}

// An open account gives seats back, never below zero.
func TestOpenSeatsComeBackNeverBelowZero(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityOpen, 10)
	Take(db, a, 3, Entry{})
	if err := Give(db, a, 3, Entry{Reason: model.CapacityRelease}); err != nil {
		t.Fatal(err)
	}
	if err := Give(db, a, 1, Entry{Reason: model.CapacityRelease}); !errors.Is(err, ErrFull) {
		t.Fatalf("a seat given back that was never held: %v", err)
	}
	if got := reread(t, db, a); got.Used != 0 {
		t.Fatalf("used %d", got.Used)
	}
	balanced(t, db)
}

// An admin hands on what they have free and no more; what they handed is
// theirs no longer to take; a reseller's use is out of what they were given;
// only the unused part comes back.
func TestHandingDownIsASaleOfWhatIsFree(t *testing.T) {
	db := testDB(t)
	admin := account(t, db, 1, model.CapacityOpen, 100)
	Take(db, admin, 30, Entry{Reason: model.CapacityCreate})
	res := account(t, db, 2, model.CapacityOpen, 0)
	if err := HandDown(db, admin, res, 71, Entry{}); !errors.Is(err, ErrFull) {
		t.Fatalf("71 handed from 70 free: %v", err)
	}
	if err := HandDown(db, admin, res, 70, Entry{}); err != nil {
		t.Fatal(err)
	}
	if err := Take(db, admin, 1, Entry{}); !errors.Is(err, ErrFull) {
		t.Fatalf("the admin took a unit they handed on: %v", err)
	}
	Take(db, res, 50, Entry{})
	if err := HandBack(db, admin, res, 21, Entry{}); !errors.Is(err, ErrFull) {
		t.Fatalf("21 taken back of 20 unused: %v", err)
	}
	if err := HandBack(db, admin, res, 20, Entry{}); err != nil {
		t.Fatal(err)
	}
	a, r := reread(t, db, admin), reread(t, db, res)
	if a.Handed != 50 || Free(a) != 20 || r.Granted != 50 || Free(r) != 0 {
		t.Fatalf("admin %+v reseller %+v", a, r)
	}
	balanced(t, db)
}

// A debit may leave less than is in use -- nobody is cut off -- but not
// below zero, and nothing new is taken until something is free.
func TestADebitBelowUseCutsNobodyOff(t *testing.T) {
	db := testDB(t)
	a := account(t, db, 7, model.CapacityOpen, 10)
	Take(db, a, 8, Entry{})
	if err := Debit(db, a, 5, Entry{Reason: model.CapacityDebit}); err != nil {
		t.Fatal(err)
	}
	if err := Debit(db, a, 6, Entry{Reason: model.CapacityDebit}); !errors.Is(err, ErrFull) {
		t.Fatalf("debited below zero: %v", err)
	}
	if err := Take(db, a, 1, Entry{}); !errors.Is(err, ErrFull) {
		t.Fatalf("taken while over: %v", err)
	}
	if got := reread(t, db, a); got.Granted != 5 || got.Used != 8 {
		t.Fatalf("%+v", got)
	}
	balanced(t, db)
}
