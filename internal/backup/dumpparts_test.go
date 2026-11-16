package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// sqliteDB is a migrated database on disk with rows enough that its dump
// is several parts: settings of four kilobytes each, past three parts.
func sqliteDB(t *testing.T, rows int) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "src.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = raw.Close() })
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 4096)
	var ss []model.Setting
	for i := 0; i < rows; i++ {
		ss = append(ss, model.Setting{Key: fmt.Sprintf("k%06d", i), Value: pad})
	}
	if err := db.CreateInBatches(ss, 200).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Client{Name: "alice", SubToken: "tok", UsedBytes: 1<<53 + 1}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// dumpService is a backup service for a PostgreSQL-like panel: no SQLite
// file, the dump is the database.
func dumpService(t *testing.T, db *gorm.DB) (*Service, string) {
	t.Helper()
	data := t.TempDir()
	return New(Options{DataDir: data, Log: quiet(), Dump: func(ctx context.Context, w io.Writer) error {
		_, err := database.WriteDump(ctx, db, config.DriverSQLite, "test", w)
		return err
	}}), data
}

// The dump goes into the archive in parts, in order, and comes back out as
// the one stream it was: checked whole, staged, and loaded to the same rows.
func TestADumpGoesIntoAnArchiveInPartsAndComesBackWhole(t *testing.T) {
	src := sqliteDB(t, 6000) // ~24 MiB of rows: four parts
	s, _ := dumpService(t, src)
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), a.Name)

	got := entries(t, path)
	var parts []string
	for name := range got {
		if isDumpPart(name) {
			parts = append(parts, name)
		}
	}
	if len(parts) < 3 {
		t.Fatalf("the dump went in as %d parts, want several", len(parts))
	}
	for name, body := range got {
		if isDumpPart(name) && len(body) > dumpPartSize {
			t.Fatalf("%s is %d bytes, over the part size", name, len(body))
		}
	}

	sum, err := s.Check(path)
	if err != nil {
		t.Fatalf("a good archive failed the check: %v", err)
	}
	var have int64
	src.Model(&model.Setting{}).Count(&have)
	if sum == nil || sum.Rows != have+1 { // every setting, and alice
		t.Fatalf("the check counted %d rows, the database holds %d", sum.Rows, have+1)
	}

	// Staged as a restore stages it, joined, and loaded into a new database.
	staging := t.TempDir()
	if _, err := s.unpack(path, staging); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err := joinDumpParts(staging, data, parts); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(data, PendingDumpFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dst := sqliteDB(t, 0)
	loaded, err := database.LoadDump(context.Background(), dst, config.DriverSQLite, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sum.Tables {
		if loaded.Tables[i] != sum.Tables[i] {
			t.Fatalf("table %s changed between the archive and the database", sum.Tables[i].Name)
		}
	}
	var c model.Client
	if err := dst.Where("sub_token = ?", "tok").First(&c).Error; err != nil || c.UsedBytes != 1<<53+1 {
		t.Fatalf("%+v %v", c, err)
	}
}

// An archive whose dump was altered after it was written -- one byte of one
// part, or a part missing -- is refused before a restore touches anything.
func TestAnArchiveWithADamagedDumpIsRefused(t *testing.T) {
	src := sqliteDB(t, 4000)
	s, _ := dumpService(t, src)
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(s.Dir(), a.Name)

	for name, change := range map[string]func(name string, body []byte) ([]byte, bool){
		"one byte changed": func(name string, body []byte) ([]byte, bool) {
			if name == DumpDir+"/00002.jsonl" {
				b := bytes.Clone(body)
				i := bytes.IndexByte(b[100:], 'x') + 100
				b[i] = 'y'
				return b, true
			}
			return body, true
		},
		"a part missing": func(name string, body []byte) ([]byte, bool) {
			return body, name != DumpDir+"/00002.jsonl"
		},
		"the last part missing": func(name string, body []byte) ([]byte, bool) {
			return body, !(isDumpPart(name) && name == lastPart(t, good))
		},
	} {
		bad := rewrite(t, good, change)
		if _, err := s.Check(bad); err == nil {
			t.Errorf("%s: the archive passed the check", name)
		}
		if err := s.verify(bad, "x"); err == nil {
			t.Errorf("%s: a restore would have gone ahead", name)
		}
	}
}

func lastPart(t *testing.T, path string) string {
	last := ""
	for name := range entries(t, path) {
		if isDumpPart(name) && name > last {
			last = name
		}
	}
	return last
}

// rewrite copies an archive, changing or dropping members.
func rewrite(t *testing.T, path string, change func(name string, body []byte) ([]byte, bool)) string {
	t.Helper()
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := filepath.Join(t.TempDir(), "bad.tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		body, keep := change(hdr.Name, body)
		if !keep {
			continue
		}
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()
	f.Close()
	return out
}
