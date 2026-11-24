package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// withoutColumns rewrites a dump as a panel from before the columns drop
// would have written it: the columns gone from the table and its rows, and
// the table's checksum made again, so the dump is whole.
func withoutColumns(t *testing.T, raw []byte, drop map[string][]string) []byte {
	t.Helper()
	var out bytes.Buffer
	var skip map[int]bool
	var table string
	h := sha256.New()
	for _, line := range bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte(`{"table":`)):
			var open dumpTableOpen
			must(t, json.Unmarshal(line, &open))
			table, skip, h = open.Table, map[int]bool{}, sha256.New()
			var cols []string
			for i, c := range open.Columns {
				gone := false
				for _, d := range drop[open.Table] {
					gone = gone || d == c
				}
				if gone {
					skip[i] = true
					continue
				}
				cols = append(cols, c)
			}
			open.Columns = cols
			line, _ = json.Marshal(open)
		case len(line) > 0 && line[0] == '[':
			var vals []json.RawMessage
			must(t, json.Unmarshal(line, &vals))
			var kept []json.RawMessage
			for i, v := range vals {
				if !skip[i] {
					kept = append(kept, v)
				}
			}
			line, _ = json.Marshal(kept)
			h.Write(line)
			h.Write([]byte{'\n'})
		case bytes.HasPrefix(line, []byte(`{"end":`)):
			var end dumpTableEnd
			must(t, json.Unmarshal(line, &end))
			if end.End != table {
				t.Fatalf("end of %s inside %s", end.End, table)
			}
			end.SHA256 = hex.EncodeToString(h.Sum(nil))
			line, _ = json.Marshal(end)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// A backup from a panel before a column was added comes back with that
// column's default, not Go's zero: a node enabled, an interface with its
// MTU, a customer allowed their device -- on both kinds of copy.
func TestAnOlderBackupGetsTheDefaultsOfItsMissingColumns(t *testing.T) {
	src := memDB(t)
	must(t, src.Create(&model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16"}).Error)
	must(t, src.Create(&model.Client{Name: "c"}).Error)
	raw, _ := dumpOf(t, src, config.DriverSQLite)
	old := withoutColumns(t, raw, map[string][]string{
		"interfaces": {"mtu", "mode"},
		"clients":    {"device_limit"},
		"nodes":      {"enabled", "usage_coefficient", "tls_mode"},
	})

	dst := memDB(t)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(old), nil); err != nil {
		t.Fatal(err)
	}
	var in model.Interface
	must(t, dst.First(&in).Error)
	if in.MTU != 1420 || in.Mode != model.InterfaceMode("standard") {
		t.Errorf("interface came back with MTU %d, mode %q", in.MTU, in.Mode)
	}
	var c model.Client
	must(t, dst.First(&c).Error)
	if c.DeviceLimit != 1 {
		t.Errorf("customer came back allowed %d devices", c.DeviceLimit)
	}
	var nodes []model.Node
	must(t, dst.Find(&nodes).Error)
	for _, n := range nodes {
		if !n.Enabled || n.UsageCoefficient != 1 || n.TLSMode == "" {
			t.Errorf("node %d came back enabled=%v coefficient=%v tls=%q", n.ID, n.Enabled, n.UsageCoefficient, n.TLSMode)
		}
	}
	// A value the copy does have is the copy's, default or not.
	must(t, src.Model(&model.Client{}).Where("1 = 1").Update("device_limit", 7).Error)
	raw, _ = dumpOf(t, src, config.DriverSQLite)
	dst = memDB(t)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(raw), nil); err != nil {
		t.Fatal(err)
	}
	must(t, dst.First(&c).Error)
	if c.DeviceLimit != 7 {
		t.Fatalf("the copy's own value became %d", c.DeviceLimit)
	}
}

// The legacy JSON copy too: a column its rows do not name gets its default.
func TestAnOlderPortableCopyGetsTheDefaultsOfItsMissingColumns(t *testing.T) {
	doc := `{"format":1,"version":"2.0.0","driver":"sqlite","tables":{"interfaces":[{"id":1,"name":"wg0","protocol":"wireguard","listen_port":51820,"subnet":"10.66.0.0/16"}]}}`
	dst := memDB(t)
	if err := Import(context.Background(), dst, config.DriverSQLite, strings.NewReader(doc), nil); err != nil {
		t.Fatal(err)
	}
	var in model.Interface
	must(t, dst.First(&in, 1).Error)
	if in.MTU != 1420 {
		t.Fatalf("MTU %d", in.MTU)
	}
}

// A dump from a panel with database changes this one does not know is
// refused, before anything is loaded: its new tables and columns would be
// dropped without a word.
func TestADumpFromAPanelWithNewerMigrationsIsRefused(t *testing.T) {
	src := memDB(t)
	must(t, src.Create(&model.Client{Name: "theirs"}).Error)
	raw, sum := dumpOf(t, src, config.DriverSQLite)
	if sum.Header.Schema != LatestMigration() {
		t.Fatalf("the dump says schema %d, the panel knows %d", sum.Header.Schema, LatestMigration())
	}
	nl := bytes.IndexByte(raw, '\n')
	var h DumpHeader
	must(t, json.Unmarshal(raw[:nl], &h))
	h.Schema, h.Version = LatestMigration()+1, "9.9.9"
	head, _ := json.Marshal(h)
	newer := append(head, raw[nl:]...)

	if _, err := CheckDump(bytes.NewReader(newer)); !errors.Is(err, ErrNewerBackup) || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("check: %v", err)
	}
	dst := memDB(t)
	must(t, dst.Create(&model.Client{Name: "mine"}).Error)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(newer), nil); !errors.Is(err, ErrNewerBackup) {
		t.Fatalf("load: %v", err)
	}
	var names []string
	dst.Model(&model.Client{}).Pluck("name", &names)
	if len(names) != 1 || names[0] != "mine" {
		t.Fatalf("the database changed: %v", names)
	}
	// One from before the field: loaded as it always was.
	h.Schema = 0
	head, _ = json.Marshal(h)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(append(head, raw[nl:]...)), nil); err != nil {
		t.Fatalf("a dump from before the field: %v", err)
	}
}

// A backup cancelled halfway hands its connection back to the pool out of
// its transaction: the next write on it goes through.
func TestACancelledDumpLeavesItsConnectionClean(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "w.db")), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	sqlDB, err := db.DB()
	must(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	must(t, Migrate(db))
	seedEverything(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	must(t, db.Callback().Row().After("gorm:row").Register("test:cancel", func(*gorm.DB) { cancel() }))
	_, err = WriteDump(ctx, db, config.DriverSQLite, "test", &bytes.Buffer{})
	must(t, db.Callback().Row().Remove("test:cancel"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the dump was not cancelled: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			return tx.Create(&model.Client{Name: fmt.Sprintf("after-%d", i)}).Error
		}); err != nil {
			t.Fatalf("a write after the cancelled dump: %v", err)
		}
	}
}

// A NUL in text, which SQLite keeps and PostgreSQL refuses, is named in
// words where the load stops.
func TestANulInTextIsNamedOnTheWayToPostgres(t *testing.T) {
	l := newTableLoader(&model.Client{}, mustSchema(t, &model.Client{}), []string{"name"}, config.DriverPostgres)
	err := l.add(context.Background(), []any{"a\x00b"})
	if err == nil || !strings.Contains(err.Error(), "NUL") || !strings.Contains(err.Error(), "name") {
		t.Fatalf("got %v", err)
	}
	l = newTableLoader(&model.Client{}, mustSchema(t, &model.Client{}), []string{"name"}, config.DriverSQLite)
	if err := l.add(context.Background(), []any{"a\x00b"}); err != nil {
		t.Fatalf("SQLite holds it: %v", err)
	}
}

func mustSchema(t *testing.T, m any) *schema.Schema {
	t.Helper()
	sch, err := parseSchema(m)
	must(t, err)
	return sch
}

// A zero in a column with a default -- a tunnel or node switched off, a
// customer allowed no device -- comes back a zero: it used to come back as
// the default, and the load, held to the copy, refused the whole backup.
func TestAZeroWhereTheColumnHasADefaultComesBack(t *testing.T) {
	src := memDB(t)
	must(t, src.Create(&model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16"}).Error)
	must(t, src.Create(&model.Interface{Name: "wg1", Protocol: "wireguard", ListenPort: 51821, Subnet: "10.67.0.0/16"}).Error)
	must(t, src.Model(&model.Interface{}).Where("name = ?", "wg0").Updates(map[string]any{"enabled": false, "mtu": 0}).Error)
	for i := 0; i < 1200; i++ { // past one batch
		must(t, src.Create(&model.Client{Name: fmt.Sprintf("c%d", i), DeviceLimit: 1 + i%2}).Error)
	}
	must(t, src.Model(&model.Client{}).Where("device_limit = 2").Update("device_limit", 0).Error)
	must(t, src.Model(&model.Node{}).Where("1 = 1").Updates(map[string]any{"enabled": false, "usage_coefficient": 0}).Error)
	raw, sum := dumpOf(t, src, config.DriverSQLite)

	dst := memDB(t)
	if _, err := LoadDump(context.Background(), dst, config.DriverSQLite, bytes.NewReader(raw), nil); err != nil {
		t.Fatalf("a panel with a disabled tunnel did not restore: %v", err)
	}
	_, again := dumpOf(t, dst, config.DriverSQLite)
	sameTables(t, sum, again)
	var in model.Interface
	must(t, dst.Where("name = ?", "wg0").First(&in).Error)
	var on model.Interface
	must(t, dst.Where("name = ?", "wg1").First(&on).Error)
	if in.Enabled || in.MTU != 0 || !on.Enabled || on.MTU != 1420 {
		t.Fatalf("wg0 enabled=%v mtu=%d, wg1 enabled=%v mtu=%d", in.Enabled, in.MTU, on.Enabled, on.MTU)
	}
	var zero int64
	dst.Model(&model.Client{}).Where("device_limit = 0").Count(&zero)
	if zero != 600 {
		t.Fatalf("%d customers allowed no device, want 600", zero)
	}
}

// The same on PostgreSQL, from SQLite and back: zeros with defaults, and a
// panel from before a column.
func TestZerosAndOlderColumnsCrossToPostgres(t *testing.T) {
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	src := memDB(t)
	must(t, src.Create(&model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16"}).Error)
	must(t, src.Model(&model.Interface{}).Where("1 = 1").Update("enabled", false).Error)
	must(t, src.Create(&model.Client{Name: "none", DeviceLimit: 1}).Error)
	must(t, src.Model(&model.Client{}).Where("1 = 1").Update("device_limit", 0).Error)
	raw, sum := dumpOf(t, src, config.DriverSQLite)

	pg, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	must(t, Migrate(pg))
	if _, err := LoadDump(context.Background(), pg, config.DriverPostgres, bytes.NewReader(raw), nil); err != nil {
		t.Fatal(err)
	}
	_, psum := dumpOf(t, pg, config.DriverPostgres)
	sameTables(t, sum, psum)

	old := withoutColumns(t, raw, map[string][]string{"interfaces": {"mtu"}})
	if _, err := LoadDump(context.Background(), pg, config.DriverPostgres, bytes.NewReader(old), nil); err != nil {
		t.Fatal(err)
	}
	var in model.Interface
	must(t, pg.First(&in).Error)
	if in.Enabled || in.MTU != 1420 {
		t.Fatalf("enabled=%v mtu=%d", in.Enabled, in.MTU)
	}
	// Back to SQLite, exactly.
	praw, _ := dumpOf(t, pg, config.DriverPostgres)
	back := memDB(t)
	if _, err := LoadDump(context.Background(), back, config.DriverSQLite, bytes.NewReader(praw), nil); err != nil {
		t.Fatal(err)
	}
}
