package database

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Migrations only ever add. A table, a column or an index an earlier release
// created is still there, with the same type, after this release migrates its
// database: a dropped or renamed column is data an update deleted, and an
// older binary -- a node not yet updated, a downgrade after a bad release --
// reading the database finds what it expects.
//
// Checked against what each release really wrote: the upgrade fixtures,
// v1.0.0 on.
const fixtureDir = "../upgradecheck/testdata"

// droppedOnPurpose are the only things a migration has ever removed, each
// with why. Adding to this list is a decision, and its place is here, in
// review, not in a migration nobody reads.
var droppedOnPurpose = map[string]string{
	// A group's name was unique across the panel and became unique per
	// operator, so two resellers can each have a "trial" (Migrate drops it
	// by every name the drivers gave it).
	"index groups.idx_groups_name": "group names became unique per operator",
}

func TestMigrationsOnlyAdd(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(fixtureDir, "v*", "wui.db"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures under %s: %v", fixtureDir, err)
	}
	for _, src := range dirs {
		version := filepath.Base(filepath.Dir(src))
		t.Run(version, func(t *testing.T) {
			db := openCopy(t, src)
			before := schemaOf(t, db)
			if err := Migrate(db); err != nil {
				t.Fatalf("migrating the %s database: %v", version, err)
			}
			after := schemaOf(t, db)
			for item, was := range before {
				is, ok := after[item]
				switch {
				case !ok && droppedOnPurpose[item] != "":
				case !ok:
					t.Errorf("%s is gone after the migration", item)
				case !strings.EqualFold(is, was):
					t.Errorf("%s was %q, is %q", item, was, is)
				}
			}
		})
	}
}

// schemaOf lists every table, column and index: "table T", "column T.C" ->
// its declared type, "index T.I" -> its columns.
func schemaOf(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	var tables []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).
		Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		out["table "+table] = ""
		var cols []struct {
			Name string
			Type string
		}
		if err := db.Raw(`SELECT name, type FROM pragma_table_info(?)`, table).Scan(&cols).Error; err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			out["column "+table+"."+c.Name] = c.Type
		}
		var idx []struct{ Name string }
		if err := db.Raw(`SELECT name FROM pragma_index_list(?)`, table).Scan(&idx).Error; err != nil {
			t.Fatal(err)
		}
		for _, i := range idx {
			// An index SQLite makes for a primary key or a UNIQUE column
			// is named for its position, not its meaning; what it covers is
			// in the columns, checked above.
			if strings.HasPrefix(i.Name, "sqlite_autoindex_") {
				continue
			}
			var on []string
			if err := db.Raw(`SELECT name FROM pragma_index_info(?)`, i.Name).Scan(&on).Error; err != nil {
				t.Fatal(err)
			}
			sort.Strings(on)
			out["index "+table+"."+i.Name] = strings.Join(on, ",")
		}
	}
	return out
}

func openCopy(t *testing.T, src string) *gorm.DB {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "wui.db")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	out.Close()
	db, err := gorm.Open(sqlite.Open(dst), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})
	return db
}

// The schema read is what the test rests on: it sees a column, its type and
// an index, so that "nothing is gone" means something.
func TestSchemaOfSeesWhatIsThere(t *testing.T) {
	db := openCopy(t, filepath.Join(fixtureDir, "v2.6.0", "wui.db"))
	s := schemaOf(t, db)
	for _, want := range []string{"table clients", "column clients.quota_bytes", "column accounts.public_key", "index clients.idx_clients_name"} {
		if _, ok := s[want]; !ok {
			t.Errorf("schemaOf does not see %s; it sees %d items", want, len(s))
		}
	}
	if fmt.Sprint(len(s)) == "0" {
		t.Fatal("an empty schema")
	}
}
