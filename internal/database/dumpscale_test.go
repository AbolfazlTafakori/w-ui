package database

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Every table, every column, every kind of value: each table filled with
// rows whose values are drawn at random from what its columns can hold --
// the largest numbers a column stores, text in every script the panel is
// used in, empty strings, nulls, times to the microsecond -- and taken
// SQLite -> PostgreSQL -> SQLite. After each trip every table is dumped
// again and its checksum compared: one value different anywhere, in any
// row of any table, and the test fails naming the table.
//
// WUI_TEST_PG_DSN is an empty PostgreSQL database; WUI_DUMP_SCALE the rows
// per table (default 300).

var scripts = []string{"", "a", "سارا محمدی", "Анна", "张伟", "Ünïcödé", "emoji 😀🔐", "quote \" back \\ slash", "line\nbreak\ttab", "null\u0000?", strings.Repeat("long ", 200)}

// fillValue is a random value of the field's type, edges included.
func fillValue(r *rand.Rand, f *schema.Field, row int, uniqueCols map[string]bool) (reflect.Value, bool) {
	t := f.FieldType
	ptr := t.Kind() == reflect.Pointer
	if ptr {
		if r.Intn(3) == 0 {
			return reflect.Zero(t), true // null
		}
		t = t.Elem()
	}
	var v reflect.Value
	switch {
	case t == reflect.TypeOf(time.Time{}):
		// Microseconds: what PostgreSQL keeps of a time.
		at := time.Date(2000+r.Intn(40), time.Month(1+r.Intn(12)), 1+r.Intn(28), r.Intn(24), r.Intn(60), r.Intn(60), r.Intn(1e6)*1000, time.UTC)
		v = reflect.ValueOf(at)
	case reflect.PointerTo(t).Implements(scannerType):
		return reflect.Value{}, false // a JSON column: left as the model makes it
	default:
		switch t.Kind() {
		case reflect.String:
			s := scripts[r.Intn(len(scripts))]
			s = strings.ReplaceAll(s, "\u0000", "") // PostgreSQL text holds no NUL
			if f.Unique || f.PrimaryKey || uniqueCols[f.DBName] {
				s = fmt.Sprintf("%d-%s", row, s)
			}
			if f.Size > 0 && len(s) > f.Size {
				s = s[:f.Size]
				for !utf8Valid(s) {
					s = s[:len(s)-1]
				}
			}
			v = reflect.ValueOf(s).Convert(t)
		case reflect.Bool:
			v = reflect.ValueOf(r.Intn(2) == 0)
		case reflect.Int, reflect.Int64:
			n := []int64{0, -1, 1, math.MaxInt64, math.MinInt64 + 1, r.Int63(), -r.Int63()}[r.Intn(7)]
			if t.Kind() == reflect.Int && strconv.IntSize == 32 {
				n = int64(int32(n))
			}
			v = reflect.ValueOf(n).Convert(t)
		case reflect.Int8, reflect.Int16, reflect.Int32:
			v = reflect.ValueOf(int64(r.Intn(100))).Convert(t)
		case reflect.Uint, reflect.Uint64:
			n := []uint64{0, 1, math.MaxInt64, 1<<53 + 1, uint64(r.Int63())}[r.Intn(5)]
			v = reflect.ValueOf(n).Convert(t)
		case reflect.Uint8, reflect.Uint16, reflect.Uint32:
			v = reflect.ValueOf(uint64(r.Intn(60000))).Convert(t)
		case reflect.Float32, reflect.Float64:
			v = reflect.ValueOf([]float64{0, 0.1, 1.5, 1e-9, 12345.678901234, -3.25}[r.Intn(6)]).Convert(t)
		case reflect.Slice:
			if t.Elem().Kind() == reflect.String {
				return reflect.Value{}, false // a list kept in another table
			}
			return reflect.Value{}, false
		default:
			return reflect.Value{}, false
		}
	}
	if ptr {
		p := reflect.New(t)
		p.Elem().Set(v)
		return p, true
	}
	return v, true
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

// fillAll puts rows rows in every table, ids 1..rows, every reference to
// another table pointing at an id that exists there.
func fillAll(t *testing.T, db *gorm.DB, rows int) {
	t.Helper()
	r := rand.New(rand.NewSource(42))
	for _, m := range model.AllModels() {
		sch, err := parseSchema(m)
		must(t, err)
		uniqueCols := map[string]bool{}
		for _, idx := range sch.ParseIndexes() {
			if idx.Class == "UNIQUE" || idx.Option == "UNIQUE" {
				for _, fo := range idx.Fields {
					uniqueCols[fo.DBName] = true
				}
			}
		}
		elem := reflect.TypeOf(m).Elem()
		batch := reflect.MakeSlice(reflect.SliceOf(elem), 0, 200)
		for i := 1; i <= rows; i++ {
			rv := reflect.New(elem).Elem()
			for _, f := range dumpColumns(sch) {
				if f.PrimaryKey && f.AutoIncrement || f.PrimaryKey && f.FieldType.Kind() == reflect.Uint && len(sch.PrimaryFields) == 1 {
					must(t, f.Set(context.Background(), rv, uint64(i)))
					continue
				}
				// A reference: an id that exists, 1..rows.
				if strings.HasSuffix(f.Name, "ID") && f.Name != "ID" && (f.FieldType.Kind() == reflect.Uint || f.FieldType.Kind() == reflect.Uint32) {
					ref := uint64(1 + r.Intn(rows))
					if f.PrimaryKey {
						ref = uint64(i) // a key of two columns: unique with the other
					}
					must(t, f.Set(context.Background(), rv, ref))
					continue
				}
				if v, ok := fillValue(r, f, i, uniqueCols); ok {
					must(t, f.Set(context.Background(), rv, v.Interface()))
				}
			}
			batch = reflect.Append(batch, rv)
			if batch.Len() == 200 || i == rows {
				if err := db.Session(&gorm.Session{CreateBatchSize: 200}).Select("*").Create(batch.Interface()).Error; err != nil {
					t.Fatalf("fill %s: %v", sch.Table, err)
				}
				batch = reflect.MakeSlice(reflect.SliceOf(elem), 0, 200)
			}
		}
	}
}

func pgFresh(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	pg, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	// Empty, whatever an earlier run left.
	must(t, pg.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public").Error)
	must(t, Migrate(pg))
	return pg
}

func TestEveryTableAndValueRoundTripsAcrossEngines(t *testing.T) {
	rows := 300
	if v := os.Getenv("WUI_DUMP_SCALE"); v != "" {
		rows, _ = strconv.Atoi(v)
	}
	pg := pgFresh(t)
	ctx := context.Background()

	src := memDB(t)
	must(t, src.Exec("PRAGMA foreign_keys = OFF").Error)
	fillAll(t, src, rows)
	must(t, src.Exec("PRAGMA foreign_keys = ON").Error)
	// Loaded once, so the data migrations a load runs have run; from here
	// every trip must leave every value as it is.
	first, _ := dumpOf(t, src, config.DriverSQLite)
	a := memDB(t)
	if _, err := LoadDump(ctx, a, config.DriverSQLite, bytes.NewReader(first), nil); err != nil {
		t.Fatal(err)
	}
	rawA, sumA := dumpOf(t, a, config.DriverSQLite)
	for _, tb := range sumA.Tables {
		if tb.Rows == 0 {
			t.Errorf("table %s was not filled", tb.Name)
		}
	}

	if _, err := LoadDump(ctx, pg, config.DriverPostgres, bytes.NewReader(rawA), nil); err != nil {
		t.Fatalf("into PostgreSQL: %v", err)
	}
	rawP, sumP := dumpOf(t, pg, config.DriverPostgres)
	sameTables(t, sumA, sumP)

	back := memDB(t)
	if _, err := LoadDump(ctx, back, config.DriverSQLite, bytes.NewReader(rawP), nil); err != nil {
		t.Fatalf("back into SQLite: %v", err)
	}
	_, sumB := dumpOf(t, back, config.DriverSQLite)
	sameTables(t, sumA, sumB)
	t.Logf("%d tables, %d rows, every value the same after SQLite -> PostgreSQL -> SQLite", len(sumA.Tables), sumA.Rows)

	// And the panel can go on writing in PostgreSQL after the load.
	if err := pg.Create(&model.Client{Name: "after"}).Error; err != nil {
		t.Fatalf("an insert after the load: %v", err)
	}
	var sqlDB *sql.DB
	sqlDB, _ = pg.DB()
	_ = sqlDB.Close()
}
