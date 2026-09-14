package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// A backup has to outlive the database engine it was taken on. An operator who
// starts on SQLite and moves to PostgreSQL when the customer count grows, or
// who restores last year's archive into this year's panel, must get their
// customers back either way. The SQLite file in the archive is exact but is
// only worth something to another SQLite install; this is the copy that works
// everywhere.
//
// The dump goes through GORM's schema rather than encoding/json on the models:
// the models hide every secret from the API with json:"-", and a backup with
// no private keys in it is not a backup.

// PortableFormat is bumped only when an older panel could not read the file.
// Adding a column does not bump it: a column the reader does not know is
// skipped, one the file does not have stays at its zero value.
const PortableFormat = 1

// Dump is the on-disk shape.
type Dump struct {
	Format  int                         `json:"format"`
	Version string                      `json:"version"`
	Driver  string                      `json:"driver"`
	TakenAt time.Time                   `json:"takenAt"`
	Tables  map[string][]map[string]any `json:"tables"`
}

var (
	schemaCache sync.Map
	namer       = schema.NamingStrategy{}
)

func parseSchema(m any) (*schema.Schema, error) {
	return schema.Parse(m, &schemaCache, namer)
}

// Export writes every table as JSON.
func Export(ctx context.Context, db *gorm.DB, driver config.Driver, version string, w io.Writer) error {
	dump := Dump{
		Format:  PortableFormat,
		Version: version,
		Driver:  string(driver),
		TakenAt: time.Now().UTC(),
		Tables:  map[string][]map[string]any{},
	}
	for _, m := range model.AllModels() {
		sch, err := parseSchema(m)
		if err != nil {
			return fmt.Errorf("database: export: %w", err)
		}
		rows, err := exportTable(ctx, db, m, sch)
		if err != nil {
			return fmt.Errorf("database: export %s: %w", sch.Table, err)
		}
		dump.Tables[sch.Table] = rows
	}
	enc := json.NewEncoder(w)
	return enc.Encode(dump)
}

func exportTable(ctx context.Context, db *gorm.DB, m any, sch *schema.Schema) ([]map[string]any, error) {
	elem := reflect.TypeOf(m).Elem()
	rows := []map[string]any{}
	// In batches, so a year of traffic samples does not have to fit in memory
	// twice over.
	const batch = 1000
	slicePtr := reflect.New(reflect.SliceOf(elem))
	err := db.WithContext(ctx).Model(m).Order(clause.OrderByColumn{Column: clause.Column{Name: clause.PrimaryKey}}).
		FindInBatches(slicePtr.Interface(), batch, func(tx *gorm.DB, _ int) error {
			s := slicePtr.Elem()
			for i := 0; i < s.Len(); i++ {
				rv := s.Index(i)
				row := make(map[string]any, len(sch.Fields))
				for _, f := range sch.Fields {
					if f.DBName == "" || f.IgnoreMigration {
						continue
					}
					v, zero := f.ValueOf(ctx, rv)
					if zero && !f.PrimaryKey {
						continue
					}
					// A JSON column and the like: archive what the database
					// holds, not the Go value behind it.
					if valuer, ok := v.(driver.Valuer); ok {
						dv, err := valuer.Value()
						if err != nil {
							return fmt.Errorf("%s: %w", f.DBName, err)
						}
						v = dv
					}
					row[f.DBName] = v
				}
				rows = append(rows, row)
			}
			return nil
		}).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Import replaces the database's contents with the dump's.
//
// Everything happens in one transaction: either the whole archive is in, or
// nothing changed. The schema has already been migrated by Open, so a dump from
// an older panel lands in the current tables; what it does not mention stays
// at its default.
func Import(ctx context.Context, db *gorm.DB, driver config.Driver, r io.Reader, log *slog.Logger) error {
	var dump Dump
	if err := json.NewDecoder(r).Decode(&dump); err != nil {
		return fmt.Errorf("database: import: not a portable backup: %w", err)
	}
	if dump.Format > PortableFormat {
		return fmt.Errorf("database: import: this backup was written by a newer panel (format %d); update first", dump.Format)
	}

	models := model.AllModels()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if driver == config.DriverSQLite {
			// Foreign keys are checked at the end instead of row by row, so
			// the load order inside a table does not matter.
			if err := tx.Exec("PRAGMA defer_foreign_keys = ON").Error; err != nil {
				return err
			}
		}
		// Children before parents on the way out.
		for i := len(models) - 1; i >= 0; i-- {
			sch, err := parseSchema(models[i])
			if err != nil {
				return err
			}
			if err := tx.Table(sch.Table).Where("1 = 1").Delete(nil).Error; err != nil {
				return fmt.Errorf("clear %s: %w", sch.Table, err)
			}
		}
		// Parents before children on the way in.
		total := 0
		for _, m := range models {
			sch, err := parseSchema(m)
			if err != nil {
				return err
			}
			rows := dump.Tables[sch.Table]
			n, err := importTable(ctx, tx, m, sch, rows)
			if err != nil {
				return fmt.Errorf("load %s: %w", sch.Table, err)
			}
			total += n
			if driver == config.DriverPostgres && sch.PrioritizedPrimaryField != nil && sch.PrioritizedPrimaryField.AutoIncrement {
				// Rows came in with their own ids; the sequence has to move
				// past them or the next insert collides.
				q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false)`,
					sch.Table, sch.PrioritizedPrimaryField.DBName, sch.PrioritizedPrimaryField.DBName, sch.Table)
				if err := tx.Exec(q).Error; err != nil {
					return fmt.Errorf("reset sequence for %s: %w", sch.Table, err)
				}
			}
		}
		if log != nil {
			log.Info("imported a portable backup", "rows", total, "from", dump.Driver, "version", dump.Version, "takenAt", dump.TakenAt)
		}
		return nil
	})
}

func importTable(ctx context.Context, tx *gorm.DB, m any, sch *schema.Schema, rows []map[string]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	elem := reflect.TypeOf(m).Elem()
	byColumn := map[string]*schema.Field{}
	for _, f := range sch.Fields {
		if f.DBName != "" {
			byColumn[f.DBName] = f
		}
	}
	const batch = 500
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		slice := reflect.MakeSlice(reflect.SliceOf(elem), 0, end-start)
		for _, row := range rows[start:end] {
			rv := reflect.New(elem)
			for col, val := range row {
				f, ok := byColumn[col]
				if !ok || val == nil {
					continue // a column this panel no longer has
				}
				if err := setColumn(ctx, f, rv, val); err != nil {
					return 0, fmt.Errorf("column %s: %w", col, err)
				}
			}
			slice = reflect.Append(slice, rv.Elem())
		}
		// Create, not Save: the ids in the dump are kept, and every column is
		// written, zero values included, so hooks and defaults do not drift
		// the data from what was archived.
		if err := tx.Select("*").Create(slice.Interface()).Error; err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

var scannerType = reflect.TypeOf((*sql.Scanner)(nil)).Elem()

// setColumn puts what JSON gave us -- float64 for every number, strings for
// times and for JSON columns -- into the field's own type.
func setColumn(ctx context.Context, f *schema.Field, rv reflect.Value, val any) error {
	target := f.ReflectValueOf(ctx, rv)
	if target.CanAddr() && target.Addr().Type().Implements(scannerType) {
		return target.Addr().Interface().(sql.Scanner).Scan(val)
	}
	switch f.FieldType.Kind() {
	case reflect.Bool:
		if n, ok := val.(float64); ok {
			val = n != 0
		}
	}
	if f.DataType == schema.Time {
		if s, ok := val.(string); ok {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return err
			}
			val = t
		}
	}
	return f.Set(ctx, rv, val)
}
