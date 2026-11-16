package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// seedEverything fills a database with what a busy panel has: values at the
// edges of their types, times to the microsecond, nulls, JSON columns, and
// more than a thousand rows in the tables whose key is two columns.
func seedEverything(t *testing.T, db *gorm.DB) {
	t.Helper()
	at := time.Date(2026, 11, 16, 21, 34, 5, 123456000, time.UTC)
	iface := model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16",
		PrivateKey: "priv", PublicKey: "pub", AWG: model.JSON(model.AWGParams{Jc: 7, Jmin: 40, Jmax: 70})}
	must(t, db.Create(&iface).Error)
	big := uint64(math.MaxInt64 - 7) // the most a column holds, beyond what a float64 holds exactly
	c := model.Client{Name: "Аня · سارا · 张伟 \"q\" \\ \n", SubToken: "tok", QuotaBytes: big, UsedBytes: 1<<53 + 1,
		UpBytes: 1<<53 + 3, DownBytes: big - 1, ExpiresAt: &at, StartOnFirstUse: true, DurationDays: 30, DurationHours: 5,
		DeviceLimit: 50, RateBitsPerSec: 1<<62 + 1, Status: model.StatusDisabled, TelegramID: math.MaxInt64}
	must(t, db.Create(&c).Error)
	none := model.Client{Name: "empty"} // every nullable left null
	must(t, db.Create(&none).Error)
	var accs []model.Account
	for i := 0; i < 1200; i++ {
		accs = append(accs, model.Account{ClientID: c.ID, InterfaceID: iface.ID, DeviceName: fmt.Sprintf("user-%d", i%10+1),
			IP: fmt.Sprintf("10.66.%d.%d", i/250, i%250+2), PrivateKey: fmt.Sprintf("k%d", i), UpBytes: big - uint64(i)})
	}
	must(t, db.CreateInBatches(accs, 400).Error)
	var groups []model.ClientGroup
	var eps []model.AccountEndpoint
	for i := 0; i < 1500; i++ {
		groups = append(groups, model.ClientGroup{ClientID: c.ID, Name: fmt.Sprintf("g%04d", i)})
		eps = append(eps, model.AccountEndpoint{AccountID: accs[i%len(accs)].ID, Addr: fmt.Sprintf("203.0.%d.%d", i/250, i%250)})
	}
	must(t, db.CreateInBatches(groups, 500).Error)
	must(t, db.CreateInBatches(eps, 500).Error)
	must(t, db.Create(&model.UserSub{ClientID: c.ID, Device: "user-2", Token: "ut"}).Error)
	must(t, db.Create(&model.Setting{Key: "sub.title", Value: "W-UI ✓", UpdatedAt: at}).Error)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func dumpOf(t *testing.T, db *gorm.DB, drv config.Driver) ([]byte, *DumpSummary) {
	t.Helper()
	var buf bytes.Buffer
	sum, err := WriteDump(context.Background(), db, drv, "test", &buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), sum
}

// Loaded and dumped again, every table has the same checksum: every value
// of every row came back exactly as it was, the largest counters included.
func TestADumpComesBackExactly(t *testing.T) {
	src := memDB(t)
	seedEverything(t, src)
	raw, sum := dumpOf(t, src, config.DriverSQLite)

	if sum.Rows < 4000 {
		t.Fatalf("only %d rows dumped", sum.Rows)
	}
	dst := memDB(t)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(raw), nil); err != nil {
		t.Fatal(err)
	}
	_, again := dumpOf(t, dst, config.DriverSQLite)
	sameTables(t, sum, again)

	var c model.Client
	must(t, dst.Where("sub_token = ?", "tok").First(&c).Error)
	if c.QuotaBytes != math.MaxInt64-7 || c.UsedBytes != 1<<53+1 || c.RateBitsPerSec != 1<<62+1 || c.TelegramID != math.MaxInt64 {
		t.Fatalf("counters changed: %+v", c)
	}
	var e model.Client
	must(t, dst.Where("name = ?", "empty").First(&e).Error)
	if e.ExpiresAt != nil || e.ActivatedAt != nil {
		t.Fatal("a null came back as a value")
	}
	var n int64
	dst.Model(&model.ClientGroup{}).Count(&n)
	if n != 1500 {
		t.Fatalf("%d of 1500 group rows", n)
	}
}

func sameTables(t *testing.T, a, b *DumpSummary) {
	t.Helper()
	if len(a.Tables) != len(b.Tables) {
		t.Fatalf("%d tables, then %d", len(a.Tables), len(b.Tables))
	}
	for i := range a.Tables {
		if a.Tables[i] != b.Tables[i] {
			t.Errorf("table %s changed on the way: %+v -> %+v", a.Tables[i].Name, a.Tables[i], b.Tables[i])
		}
	}
}

// A dump is checked as it is loaded, and anything wrong with it leaves the
// database exactly as it was.
func TestADamagedDumpChangesNothing(t *testing.T) {
	src := memDB(t)
	seedEverything(t, src)
	raw, _ := dumpOf(t, src, config.DriverSQLite)

	cut := raw[:len(raw)/2]
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	flip := bytes.Clone(raw)
	i := bytes.Index(flip, []byte(`"tok"`))
	flip[i+1] = 'x' // one value changed, the row's shape intact
	noEnd := bytes.Join(lines[:len(lines)-1], []byte("\n"))
	dropRow := append(bytes.Join(append(append([][]byte{}, lines[:3]...), lines[4:]...), []byte("\n")), '\n')
	extra := append(bytes.Clone(raw), []byte("[1]\n")...)

	for name, bad := range map[string][]byte{
		"cut in half": cut, "a value changed": flip, "no complete line": noEnd,
		"a row removed": dropRow, "something after the end": extra, "not a dump": []byte(`{"format":1,"tables":{}}`),
	} {
		dst := memDB(t)
		mark := model.Client{Name: "already here"}
		must(t, dst.Create(&mark).Error)
		if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(bad), nil); !errors.Is(err, ErrDumpDamaged) {
			t.Errorf("%s: loaded, or refused for the wrong reason: %v", name, err)
		}
		if _, err := CheckDump(bytes.NewReader(bad)); !errors.Is(err, ErrDumpDamaged) {
			t.Errorf("%s: passed the check: %v", name, err)
		}
		var cs []model.Client
		dst.Find(&cs)
		if len(cs) != 1 || cs[0].Name != "already here" {
			t.Errorf("%s: the database changed: %d clients", name, len(cs))
		}
	}
	if _, err := CheckDump(bytes.NewReader(raw)); err != nil {
		t.Fatalf("a good dump failed the check: %v", err)
	}
}

func TestADumpFromANewerPanelIsRefused(t *testing.T) {
	newer := []byte(`{"wui":"dump","format":99,"version":"9","driver":"sqlite","takenAt":"2030-01-01T00:00:00Z"}` + "\n")
	if _, err := CheckDump(bytes.NewReader(newer)); err == nil || !strings.Contains(err.Error(), "newer panel") {
		t.Fatalf("got %v", err)
	}
}

// The way that matters most for moving engines: SQLite into PostgreSQL and
// back, every value surviving both trips. Times are kept to the microsecond,
// which is what PostgreSQL holds.
func TestADumpCrossesEnginesBothWays(t *testing.T) {
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	src := memDB(t)
	seedEverything(t, src)
	// Times made by the panel carry nanoseconds; PostgreSQL keeps micro.
	for _, tbl := range []string{"clients", "accounts", "interfaces", "user_subs", "settings"} {
		src.Exec("UPDATE " + tbl + " SET created_at = '2026-11-16 21:34:05.123456+00:00' WHERE 1=1")
		src.Exec("UPDATE " + tbl + " SET updated_at = '2026-11-16 21:34:05.123456+00:00' WHERE 1=1")
	}
	raw, sum := dumpOf(t, src, config.DriverSQLite)

	pg, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	must(t, Migrate(pg))
	if _, err := LoadDump(ctx, pg, config.DriverPostgres, bytes.NewReader(raw), nil); err != nil {
		t.Fatal(err)
	}
	praw, psum := dumpOf(t, pg, config.DriverPostgres)
	sameTables(t, sum, psum)
	if err := pg.Create(&model.Client{Name: "after the load"}).Error; err != nil {
		t.Fatalf("an insert after the load: %v", err)
	}

	back := memDB(t)
	praw2, _ := dumpOf(t, pg, config.DriverPostgres)
	if _, err := LoadDump(ctx, back, config.DriverSQLite, bytes.NewReader(praw2), nil); err != nil {
		t.Fatal(err)
	}
	var n int64
	back.Model(&model.Client{}).Count(&n)
	if n != 3 {
		t.Fatalf("%d clients back in SQLite", n)
	}
	_ = praw
}

// A dump whose file checks out but whose values the engine does not keep as
// they are -- here a time finer than PostgreSQL holds, which it would round
// without a word -- is refused when the load reads back what it wrote, and
// nothing is committed.
func TestALoadThatTheDatabaseDoesNotKeepExactlyIsRefused(t *testing.T) {
	pg := pgFresh(t)
	src := memDB(t)
	seedEverything(t, src)
	raw, _ := dumpOf(t, src, config.DriverSQLite)

	// One client's created_at made finer than a microsecond, and the
	// clients table's checksum written again to match: the file is good.
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	in := false
	h := sha256.New()
	changed := false
	for i, l := range lines {
		switch {
		case bytes.HasPrefix(l, []byte(`{"table":"clients"`)):
			in = true
		case in && bytes.HasPrefix(l, []byte(`{"end":"clients"`)):
			var end dumpTableEnd
			must(t, json.Unmarshal(l, &end))
			end.SHA256 = hex.EncodeToString(h.Sum(nil))
			b, _ := json.Marshal(end)
			lines[i] = b
			in = false
		case in:
			if !changed {
				re := regexp.MustCompile(`"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z"`)
				if loc := re.FindIndex(l); loc != nil {
					l = append(append(append([]byte{}, l[:loc[0]]...), []byte(`"2026-11-16T21:34:05.123456789Z"`)...), l[loc[1]:]...)
					lines[i] = l
					changed = true
				}
			}
			h.Write(l)
			h.Write([]byte{'\n'})
		}
	}
	if !changed {
		t.Fatal("no time to change")
	}
	bad := append(bytes.Join(lines, []byte("\n")), '\n')
	if _, err := CheckDump(bytes.NewReader(bad)); err != nil {
		t.Fatalf("the crafted file should check out: %v", err)
	}
	must(t, pg.Create(&model.Client{Name: "already here"}).Error)
	if _, err := LoadDump(context.Background(), pg, config.DriverPostgres, bytes.NewReader(bad), nil); !errors.Is(err, ErrLoadMismatch) {
		t.Fatalf("a value PostgreSQL changed was committed, or refused for the wrong reason: %v", err)
	}
	var cs []model.Client
	pg.Find(&cs)
	if len(cs) != 1 || cs[0].Name != "already here" {
		t.Fatalf("the database changed: %d clients", len(cs))
	}
}
