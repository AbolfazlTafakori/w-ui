package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// A write refused because another writer holds the database is told apart
// from any other fault: the panel can say "busy, nothing changed, try again"
// rather than "unexpected". Made for real here: one connection holds the
// write lock, the other waits its short busy_timeout out.
func TestAWriteRefusedForABusyDatabaseIsToldApart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	open := func(wait int) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path+fmt.Sprintf("?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_txlock=immediate", wait)),
			&gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
		return db
	}
	holder, writer := open(5000), open(100)
	if err := holder.Exec("CREATE TABLE t (v INTEGER)").Error; err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO t VALUES (1)").Error; err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := writer.WithContext(ctx).Exec("INSERT INTO t VALUES (2)").Error
	close(release)
	if herr := <-done; herr != nil {
		t.Fatal(herr)
	}
	if err == nil {
		t.Fatal("a write went through while another writer held the database")
	}
	if !IsBusy(err) || !IsBusy(fmt.Errorf("service: create client: %w", err)) {
		t.Fatalf("a busy database was not told apart: %v", err)
	}
	if IsBusy(errors.New("no such table: clients")) || IsBusy(nil) {
		t.Fatal("another fault was taken for a busy database")
	}
}
