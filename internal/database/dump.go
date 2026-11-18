package database

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// The database as a stream: the portable copy a backup carries, and what
// moves a panel from one engine to the other.
//
// The first portable format (Export, Import) built every table in memory and
// the whole file in a buffer before a byte was written -- a panel of twenty
// thousand customers held more than a gigabyte for a backup of a hundred
// megabytes -- read each table at its own moment, so a backup taken while
// customers were being changed could hold a file without its customer, read
// numbers back as float64, which cannot hold every byte counter exactly, and
// could not page through a table whose key is two columns at all: past a
// thousand rows of client_groups or account_endpoints the export failed, and
// on PostgreSQL, where it is the only copy, so did the backup.
//
// This one is written as it is read: one line of JSON per row, a table at a
// time, from one read-only snapshot of the whole database, so it is
// consistent whatever is changing and its memory does not grow with the
// panel. Every number is written exactly as stored and read back exactly.
// Every table ends with how many rows it has and the SHA-256 of their lines,
// and the stream ends with a line saying it is complete: a load checks both
// as it goes and, being one transaction, leaves the database as it was if
// anything does not match -- a cut, a corrupted or a tampered file is
// refused, never half-loaded.
//
// The lines:
//
//	{"wui":"dump","format":2,"version":"3.2.1","driver":"postgres","takenAt":"..."}
//	{"table":"clients","columns":["id","name",...]}
//	[1,"alice",...]                      one per row, in column order
//	{"end":"clients","rows":1,"sha256":"..."}
//	...
//	{"complete":true,"tables":24,"rows":12345}
//
// A value is JSON's own for strings, booleans and null; a number exactly as
// the column holds it; a time as RFC 3339 with nanoseconds; bytes as
// {"b":"<base64>"}.

// DumpFormat is the stream's format. A reader refuses a newer one.
const DumpFormat = 2

// DumpMagic opens every dump, so a file is known for one by its first bytes.
const DumpMagic = `{"wui":"dump"`

// dumpRead is how many rows are read from the database at a time, and
// dumpPause the rest given to the panel between reads, so a backup of a large
// panel never takes the database from customers' requests for long.
const (
	dumpRead  = 2000
	dumpPause = 2 * time.Millisecond
)

// DumpHeader is a dump's first line.
type DumpHeader struct {
	Magic   string    `json:"wui"`
	Format  int       `json:"format"`
	Version string    `json:"version"`
	Driver  string    `json:"driver"`
	TakenAt time.Time `json:"takenAt"`
}

// DumpTable is what a dump says of one table, from its closing line.
type DumpTable struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

// DumpSummary is what a dump holds, as written or as checked.
type DumpSummary struct {
	Header DumpHeader  `json:"header"`
	Tables []DumpTable `json:"tables"`
	Rows   int64       `json:"rows"`
}

type dumpTableOpen struct {
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

type dumpTableEnd struct {
	End    string `json:"end"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

type dumpComplete struct {
	Complete bool  `json:"complete"`
	Tables   int   `json:"tables"`
	Rows     int64 `json:"rows"`
}

// WriteDump streams the whole database to w.
//
// Everything is read inside one read-only transaction -- on PostgreSQL at
// REPEATABLE READ, so every table is seen at the same instant; on SQLite a
// read transaction, which in WAL mode is a snapshot too. Readers take no
// locks that hold back the panel's writes on either.
func WriteDump(ctx context.Context, db *gorm.DB, drv config.Driver, version string, w io.Writer) (*DumpSummary, error) {
	bw := bufio.NewWriterSize(w, 256<<10)
	sum := &DumpSummary{Header: DumpHeader{Magic: "dump", Format: DumpFormat, Version: version, Driver: string(drv), TakenAt: time.Now().UTC()}}
	if err := writeLine(bw, sum.Header); err != nil {
		return nil, err
	}
	run := func(tx *gorm.DB) error {
		for _, m := range model.AllModels() {
			t, err := dumpTable(ctx, tx, drv, m, bw)
			if err != nil {
				return err
			}
			sum.Tables = append(sum.Tables, *t)
			sum.Rows += t.Rows
		}
		return nil
	}
	var err error
	if drv == config.DriverPostgres {
		err = db.WithContext(ctx).Transaction(run, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	} else {
		err = sqliteReadSnapshot(ctx, db, run)
	}
	if err != nil {
		return nil, fmt.Errorf("database: dump: %w", err)
	}
	if err := writeLine(bw, dumpComplete{Complete: true, Tables: len(sum.Tables), Rows: sum.Rows}); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("database: dump: %w", err)
	}
	return sum, nil
}

// sqliteReadSnapshot runs fn on one connection inside a deferred transaction:
// a snapshot from its first read, and no lock that holds a writer back. The
// panel's own transactions begin IMMEDIATE (sqliteDSN), taking the write
// lock at once; a backup reading for a minute that way would stop every
// write on the panel for that minute.
func sqliteReadSnapshot(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("BEGIN DEFERRED").Error; err != nil {
			return err
		}
		// A new session on the pinned connection: the handle Connection hands
		// over carries its statement from call to call, and each table's
		// query would carry the one before it -- the last table's ORDER BY.
		err := fn(conn.Session(&gorm.Session{NewDB: true, Context: ctx}))
		// Nothing was written; ending it either way lets the connection go
		// back to the pool clean.
		if rerr := conn.Exec("ROLLBACK").Error; err == nil && rerr != nil {
			err = rerr
		}
		return err
	})
}

// dumpColumns are the columns of a table a dump carries, in a fixed order.
func dumpColumns(sch *schema.Schema) []*schema.Field {
	var out []*schema.Field
	for _, f := range sch.Fields {
		if f.DBName != "" && !f.IgnoreMigration {
			out = append(out, f)
		}
	}
	return out
}

// pkOrder is the table's whole primary key as an ORDER BY: every column of
// it, so a key of two columns orders as fully as a key of one. Text is
// ordered by its bytes on both engines -- PostgreSQL otherwise orders by the
// server's language rules, SQLite by bytes -- so a table dumps in the same
// order whichever engine holds it, and its checksum says the same thing.
func pkOrder(sch *schema.Schema, drv config.Driver) string {
	var b bytes.Buffer
	for i, f := range sch.PrimaryFields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.DBName)
		if drv == config.DriverPostgres && f.FieldType.Kind() == reflect.String {
			b.WriteString(` COLLATE "C"`)
		}
	}
	return b.String()
}

func dumpTable(ctx context.Context, tx *gorm.DB, drv config.Driver, m any, w *bufio.Writer) (*DumpTable, error) {
	sch, err := parseSchema(m)
	if err != nil {
		return nil, err
	}
	cols := dumpColumns(sch)
	names := make([]string, len(cols))
	for i, f := range cols {
		names[i] = f.DBName
	}
	if err := writeLine(w, dumpTableOpen{Table: sch.Table, Columns: names}); err != nil {
		return nil, err
	}

	// One query, read as it arrives: no paging, so no key is needed to page
	// by, and no row is read twice or missed between pages.
	q := tx.Model(m)
	if order := pkOrder(sch, drv); order != "" {
		q = q.Order(order)
	}
	rows, err := q.Rows()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sch.Table, err)
	}
	defer rows.Close()

	h := sha256.New()
	elem := reflect.TypeOf(m).Elem()
	var line bytes.Buffer
	var n int64
	vals := make([]any, len(cols))
	for rows.Next() {
		rv := reflect.New(elem)
		if err := tx.ScanRows(rows, rv.Interface()); err != nil {
			return nil, fmt.Errorf("read %s: %w", sch.Table, err)
		}
		for i, f := range cols {
			v, err := dumpValue(ctx, f, rv.Elem())
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", sch.Table, f.DBName, err)
			}
			vals[i] = v
		}
		line.Reset()
		if err := encodeRow(&line, vals); err != nil {
			return nil, fmt.Errorf("%s: %w", sch.Table, err)
		}
		h.Write(line.Bytes())
		if _, err := w.Write(line.Bytes()); err != nil {
			return nil, fmt.Errorf("database: dump: %w", err)
		}
		n++
		if n%dumpRead == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			time.Sleep(dumpPause)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", sch.Table, err)
	}
	t := &DumpTable{Name: sch.Table, Rows: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	if err := writeLine(w, dumpTableEnd{End: t.Name, Rows: t.Rows, SHA256: t.SHA256}); err != nil {
		return nil, err
	}
	return t, nil
}

// rawBytes marks bytes for the encoder.
type rawBytes []byte

// dumpValue is one column's value as the database holds it: what a Valuer
// hands the driver, otherwise the field itself.
func dumpValue(ctx context.Context, f *schema.Field, rv reflect.Value) (any, error) {
	v, _ := f.ValueOf(ctx, rv)
	if valuer, ok := v.(driver.Valuer); ok {
		if pv := reflect.ValueOf(v); pv.Kind() == reflect.Pointer && pv.IsNil() {
			return nil, nil
		}
		dv, err := valuer.Value()
		if err != nil {
			return nil, err
		}
		v = dv
	}
	return normalise(v), nil
}

// normalise turns a value into one of the shapes the stream writes.
func normalise(v any) any {
	if v == nil {
		return nil
	}
	if t, ok := v.(time.Time); ok {
		return t
	}
	if b, ok := v.([]byte); ok {
		return rawBytes(b)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalise(rv.Elem().Interface())
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rawBytes(rv.Bytes())
		}
	}
	return v
}

// encodeRow writes one row's line: a JSON array, numbers exact.
func encodeRow(b *bytes.Buffer, vals []any) error {
	b.WriteByte('[')
	for i, v := range vals {
		if i > 0 {
			b.WriteByte(',')
		}
		switch x := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			b.WriteString(strconv.FormatBool(x))
		case int64:
			b.WriteString(strconv.FormatInt(x, 10))
		case uint64:
			b.WriteString(strconv.FormatUint(x, 10))
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return fmt.Errorf("a number JSON cannot carry: %v", x)
			}
			b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		case time.Time:
			// To the microsecond: what both engines hold. SQLite keeps
			// nanoseconds and PostgreSQL does not, so a time written finer
			// than that would come back from PostgreSQL different, and one
			// table would checksum differently on the two engines.
			s, _ := json.Marshal(x.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
			b.Write(s)
		case rawBytes:
			b.WriteString(`{"b":"`)
			b.WriteString(base64.StdEncoding.EncodeToString(x))
			b.WriteString(`"}`)
		case string:
			s, err := json.Marshal(x)
			if err != nil {
				return err
			}
			b.Write(s)
		default:
			return fmt.Errorf("a value of type %T the dump does not carry", v)
		}
	}
	b.WriteString("]\n")
	return nil
}

func writeLine(w *bufio.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("database: dump: %w", err)
	}
	return nil
}

// ── reading ──────────────────────────────────────────────────────────────────

// ErrDumpDamaged is a dump that does not check out: cut short, altered, or
// not a dump at all.
var ErrDumpDamaged = errors.New("the backup's database copy is damaged or incomplete")

// maxDumpLine bounds one line: the largest row this panel writes is a few
// kilobytes; a line of megabytes is not one of ours.
const maxDumpLine = 16 << 20

// dumpReader walks a dump line by line, checking each table's count and
// checksum as it closes and the stream's end.
type dumpReader struct {
	sc     *bufio.Scanner
	header DumpHeader
	sum    DumpSummary
	line   int64
}

func newDumpReader(r io.Reader) (*dumpReader, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxDumpLine)
	d := &dumpReader{sc: sc}
	raw, ok := d.next()
	if !ok {
		return nil, fmt.Errorf("%w: empty", ErrDumpDamaged)
	}
	if !bytes.HasPrefix(raw, []byte(DumpMagic)) {
		return nil, fmt.Errorf("%w: not a W-UI database dump", ErrDumpDamaged)
	}
	if err := json.Unmarshal(raw, &d.header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrDumpDamaged, err)
	}
	if d.header.Format > DumpFormat {
		return nil, fmt.Errorf("database: this backup was written by a newer panel (dump format %d); update the panel first", d.header.Format)
	}
	d.sum.Header = d.header
	return d, nil
}

func (d *dumpReader) next() ([]byte, bool) {
	if !d.sc.Scan() {
		return nil, false
	}
	d.line++
	return d.sc.Bytes(), true
}

func (d *dumpReader) err(format string, a ...any) error {
	return fmt.Errorf("%w: line %d: %s", ErrDumpDamaged, d.line, fmt.Sprintf(format, a...))
}

// walk calls table for each table the dump opens, and row for each of its
// rows, decoded; it returns once the complete line is read and every count
// and checksum matched.
func (d *dumpReader) walk(table func(name string, cols []string) error, row func(vals []any) error) error {
	for {
		raw, ok := d.next()
		if !ok {
			if err := d.sc.Err(); err != nil {
				return d.err("%v", err)
			}
			return fmt.Errorf("%w: it ends before it is complete", ErrDumpDamaged)
		}
		var open dumpTableOpen
		var done dumpComplete
		if err := json.Unmarshal(raw, &done); err == nil && done.Complete {
			if done.Tables != len(d.sum.Tables) || done.Rows != d.sum.Rows {
				return d.err("it says %d tables and %d rows; it holds %d and %d", done.Tables, done.Rows, len(d.sum.Tables), d.sum.Rows)
			}
			if _, more := d.next(); more {
				return d.err("there is more after its end")
			}
			return nil
		}
		if err := json.Unmarshal(raw, &open); err != nil || open.Table == "" {
			return d.err("expected a table")
		}
		if err := table(open.Table, open.Columns); err != nil {
			return err
		}
		h := sha256.New()
		var n int64
		for {
			raw, ok := d.next()
			if !ok {
				return fmt.Errorf("%w: it ends inside table %s", ErrDumpDamaged, open.Table)
			}
			if len(raw) > 0 && raw[0] == '[' {
				h.Write(raw)
				h.Write([]byte{'\n'})
				n++
				vals, err := decodeRow(raw, len(open.Columns))
				if err != nil {
					return d.err("%s: %v", open.Table, err)
				}
				if err := row(vals); err != nil {
					return err
				}
				continue
			}
			var end dumpTableEnd
			if err := json.Unmarshal(raw, &end); err != nil || end.End != open.Table {
				return d.err("table %s is not closed", open.Table)
			}
			got := hex.EncodeToString(h.Sum(nil))
			if end.Rows != n || end.SHA256 != got {
				return d.err("table %s: it says %d rows, it holds %d, or its rows were changed", open.Table, end.Rows, n)
			}
			d.sum.Tables = append(d.sum.Tables, DumpTable{Name: open.Table, Rows: n, SHA256: got})
			d.sum.Rows += n
			break
		}
	}
}

// decodeRow reads one row's values: numbers kept as their exact text.
func decodeRow(raw []byte, want int) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var vals []any
	if err := dec.Decode(&vals); err != nil {
		return nil, err
	}
	if len(vals) != want {
		return nil, fmt.Errorf("a row of %d values for %d columns", len(vals), want)
	}
	for i, v := range vals {
		if m, ok := v.(map[string]any); ok {
			s, ok := m["b"].(string)
			if !ok || len(m) != 1 {
				return nil, fmt.Errorf("value %d is not one the dump writes", i)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("value %d: %v", i, err)
			}
			vals[i] = b
		}
	}
	return vals, nil
}

// IsDump reports whether the first bytes of a file are a dump's.
func IsDump(head []byte) bool { return bytes.HasPrefix(head, []byte(DumpMagic)) }

// CheckDump reads a whole dump without a database: every table's count and
// checksum, and that it is complete.
func CheckDump(r io.Reader) (*DumpSummary, error) {
	d, err := newDumpReader(r)
	if err != nil {
		return nil, err
	}
	if err := d.walk(func(string, []string) error { return nil }, func([]any) error { return nil }); err != nil {
		return nil, err
	}
	return &d.sum, nil
}

// LoadDump replaces the database's contents with the dump's.
//
// One transaction: the tables are emptied and filled as the dump is read,
// each table's count and checksum checked as it closes, and only a dump read
// to its complete line is committed. Anything short of that -- a damaged
// file, a value that does not fit -- rolls everything back, and the database
// is as it was. The schema is already this panel's (Open migrated it), so a
// dump from an older panel lands in the current tables: a column the dump
// does not have keeps its default, one this panel no longer has is skipped.
func LoadDump(ctx context.Context, db *gorm.DB, drv config.Driver, r io.Reader, log *slog.Logger) (*DumpSummary, error) {
	d, err := newDumpReader(r)
	if err != nil {
		return nil, err
	}
	models := model.AllModels()
	byTable := map[string]any{}
	for _, m := range models {
		sch, err := parseSchema(m)
		if err != nil {
			return nil, err
		}
		byTable[sch.Table] = m
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if drv == config.DriverSQLite {
			if err := tx.Exec("PRAGMA defer_foreign_keys = ON").Error; err != nil {
				return err
			}
		}
		for i := len(models) - 1; i >= 0; i-- {
			// The schema of a model the panel registers itself: it parses.
			sch, _ := parseSchema(models[i])
			if err := tx.Table(sch.Table).Where("1 = 1").Delete(nil).Error; err != nil {
				return fmt.Errorf("clear %s: %w", sch.Table, err)
			}
		}

		var ld *tableLoader
		// Which tables were loaded, and whether the dump's columns are this
		// panel's exactly: those are read back and checksummed below.
		loaded := map[string]bool{}
		flush := func() error {
			if ld == nil {
				return nil
			}
			return ld.flush(ctx, tx)
		}
		table := func(name string, cols []string) error {
			if err := flush(); err != nil {
				return err
			}
			if ld != nil {
				if err := ld.finish(tx, drv); err != nil {
					return err
				}
			}
			ld = nil
			m, ok := byTable[name]
			if !ok {
				return nil // a table this panel no longer has
			}
			// The schema of a model the panel registers itself: it parses.
			sch, _ := parseSchema(m)
			ld = newTableLoader(m, sch, cols)
			loaded[name] = sameColumns(sch, cols)
			return nil
		}
		row := func(vals []any) error {
			if ld == nil {
				return nil
			}
			if err := ld.add(ctx, vals); err != nil {
				return fmt.Errorf("load %s: %w", ld.sch.Table, err)
			}
			if ld.batch.Len() >= loadBatch {
				return ld.flush(ctx, tx)
			}
			return nil
		}
		if err := d.walk(table, row); err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		if ld != nil {
			if err := ld.finish(tx, drv); err != nil {
				return err
			}
		}
		// What was written is read back, before anything is committed, and
		// held to the dump: every table's rows, and where the dump's columns
		// are this panel's own, every value by its checksum. Not only was
		// the file whole -- the database now holds exactly it, on this
		// engine, and anything else is rolled back.
		if err := verifyLoaded(ctx, tx, drv, byTable, loaded, d.sum.Tables); err != nil {
			return err
		}
		// Rows from an older panel are in the shape it wrote them in; the
		// data migrations carry them into this one's.
		return Replay(tx)
	})
	if err != nil {
		return nil, err
	}
	if log != nil {
		log.Info("loaded a database dump", "tables", len(d.sum.Tables), "rows", d.sum.Rows,
			"from", d.header.Driver, "version", d.header.Version, "takenAt", d.header.TakenAt)
	}
	return &d.sum, nil
}

// ErrLoadMismatch is a database that, read back after a load, does not hold
// what the dump did.
var ErrLoadMismatch = errors.New("the database did not take the backup exactly")

// sameColumns reports whether a dump's columns are this panel's, in order.
func sameColumns(sch *schema.Schema, names []string) bool {
	cols := dumpColumns(sch)
	if len(cols) != len(names) {
		return false
	}
	for i, f := range cols {
		if f.DBName != names[i] {
			return false
		}
	}
	return true
}

// verifyLoaded reads back every table a load wrote, inside its transaction.
func verifyLoaded(ctx context.Context, tx *gorm.DB, drv config.Driver, byTable map[string]any, loaded map[string]bool, want []DumpTable) error {
	sink := bufio.NewWriterSize(io.Discard, 64<<10)
	for _, w := range want {
		exact, ok := loaded[w.Name]
		if !ok {
			continue // a table this panel no longer has
		}
		got, err := dumpTable(ctx, tx, drv, byTable[w.Name], sink)
		if err != nil {
			return fmt.Errorf("read back %s: %w", w.Name, err)
		}
		if got.Rows != w.Rows {
			return fmt.Errorf("%w: table %s holds %d rows, the backup %d", ErrLoadMismatch, w.Name, got.Rows, w.Rows)
		}
		if exact && got.SHA256 != w.SHA256 {
			return fmt.Errorf("%w: table %s does not hold the values the backup does", ErrLoadMismatch, w.Name)
		}
	}
	return nil
}

// loadBatch is how many rows go to the database in one insert.
const loadBatch = 500

type tableLoader struct {
	m     any
	sch   *schema.Schema
	elem  reflect.Type
	cols  []*schema.Field // nil where the dump's column is not this panel's
	batch reflect.Value
	rows  int64
}

func newTableLoader(m any, sch *schema.Schema, names []string) *tableLoader {
	byName := map[string]*schema.Field{}
	for _, f := range dumpColumns(sch) {
		byName[f.DBName] = f
	}
	cols := make([]*schema.Field, len(names))
	for i, n := range names {
		cols[i] = byName[n]
	}
	elem := reflect.TypeOf(m).Elem()
	return &tableLoader{m: m, sch: sch, elem: elem, cols: cols, batch: reflect.MakeSlice(reflect.SliceOf(elem), 0, loadBatch)}
}

func (l *tableLoader) add(ctx context.Context, vals []any) error {
	rv := reflect.New(l.elem)
	for i, v := range vals {
		f := l.cols[i]
		if f == nil || v == nil {
			continue
		}
		if err := setExact(ctx, f, rv, v); err != nil {
			return fmt.Errorf("column %s: %w", f.DBName, err)
		}
	}
	l.batch = reflect.Append(l.batch, rv.Elem())
	return nil
}

func (l *tableLoader) flush(_ context.Context, tx *gorm.DB) error {
	if l.batch.Len() == 0 {
		return nil
	}
	// Create, not Save: the ids are kept, and every column written, zero
	// values included, so no default or hook drifts the data from the copy.
	if err := tx.Select("*").Create(l.batch.Interface()).Error; err != nil {
		return fmt.Errorf("load %s: %w", l.sch.Table, err)
	}
	l.rows += int64(l.batch.Len())
	l.batch = reflect.MakeSlice(reflect.SliceOf(l.elem), 0, loadBatch)
	return nil
}

// finish moves a PostgreSQL sequence past the ids the table was loaded with,
// or the next insert collides with them.
func (l *tableLoader) finish(tx *gorm.DB, drv config.Driver) error {
	pk := l.sch.PrioritizedPrimaryField
	if drv != config.DriverPostgres || pk == nil || !pk.AutoIncrement {
		return nil
	}
	q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false)`,
		l.sch.Table, pk.DBName, pk.DBName, l.sch.Table)
	if err := tx.Exec(q).Error; err != nil {
		return fmt.Errorf("reset sequence for %s: %w", l.sch.Table, err)
	}
	return nil
}

// setExact puts a dumped value into a field of its own type, a number parsed
// from its exact text into the field's kind and refused if it does not fit.
func setExact(ctx context.Context, f *schema.Field, rv reflect.Value, v any) error {
	target := f.ReflectValueOf(ctx, rv)
	if target.CanAddr() && target.Addr().Type().Implements(scannerType) {
		if n, ok := v.(json.Number); ok {
			v = string(n)
		}
		return target.Addr().Interface().(sql.Scanner).Scan(v)
	}
	kind := f.FieldType.Kind()
	if kind == reflect.Pointer {
		kind = f.FieldType.Elem().Kind()
	}
	switch x := v.(type) {
	case json.Number:
		switch kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(string(x), 10, 64)
			if err != nil {
				return err
			}
			return f.Set(ctx, rv, n)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, err := strconv.ParseUint(string(x), 10, 64)
			if err != nil {
				return err
			}
			return f.Set(ctx, rv, n)
		case reflect.Float32, reflect.Float64:
			n, err := strconv.ParseFloat(string(x), 64)
			if err != nil {
				return err
			}
			return f.Set(ctx, rv, n)
		case reflect.Bool:
			return f.Set(ctx, rv, string(x) != "0")
		}
		return fmt.Errorf("a number for a %s column", kind)
	case string:
		if f.DataType == schema.Time {
			t, err := time.Parse(time.RFC3339Nano, x)
			if err != nil {
				return err
			}
			return f.Set(ctx, rv, t)
		}
	}
	return f.Set(ctx, rv, v)
}
