package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// The automatic backup went with the report before it had a time of its own.
// A panel that turned it on then keeps getting it when it always has; one
// that sets a time gets it then.
func TestTheBackupTimeStartsAsTheReportTime(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := database.PutSetting(db, keyNotifyRunTime, "0 0 8 * * *"); err != nil {
		t.Fatal(err)
	}
	if err := database.PutSetting(db, keyNotifyBackup, "true"); err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(db, "en")
	got, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NotifyBackup || got.NotifyBackupTime != "0 0 8 * * *" {
		t.Fatalf("an old panel's backup: on %v at %q, want on at the report's time", got.NotifyBackup, got.NotifyBackupTime)
	}

	got.NotifyBackupTime = "0 30 3 * * *"
	if _, err := settings.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	cfg := NewSettings(db, "en").Notify(ctx)
	if !cfg.Backup || cfg.BackupTime != "0 30 3 * * *" || cfg.RunTime != "0 0 8 * * *" {
		t.Errorf("after saving a time of its own: %+v", cfg)
	}
}

// A report sent every thirty seconds was fine; a backup that often is not,
// and taken over as the backup's time it would have had every save refused.
func TestAReportTimeTooOftenForABackupIsNotTakenOver(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := database.PutSetting(db, keyNotifyRunTime, "@every 30s"); err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(db, "en")
	got, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.NotifyBackupTime != "@daily" {
		t.Errorf("backup time %q, want @daily", got.NotifyBackupTime)
	}
	got.NotifyBackup = true
	if _, err := settings.Save(ctx, got); err != nil {
		t.Errorf("saving the page as it came: %v", err)
	}
}

func TestABackupTimeTheBotCannotKeepIsRefused(t *testing.T) {
	ctx := context.Background()
	settings := NewSettings(testDB(t), "en")
	base, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"@every 1m":  "more often than a backup may be taken, once every 10 minutes",
		"0 0 30 2 *": "does not come round within a year",
		"at three":   "backup time:",
		"":           "choose when the automatic backup is sent",
	} {
		p := base
		p.NotifyBackup, p.NotifyBackupTime = true, in
		_, err := settings.Save(ctx, p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("backup time %q: %v, want %q", in, err, want)
		}
	}
	// Off, an empty time is no one's business.
	p := base
	p.NotifyBackup, p.NotifyBackupTime = false, ""
	if _, err := settings.Save(ctx, p); err != nil {
		t.Errorf("off with no time: %v", err)
	}
}

// Where the schedule counts from is in the database, so it outlives the
// process; and the settings page saving does not overwrite it.
func TestTheBackupStampIsKept(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	settings := NewSettings(db, "en")
	if at, was, err := settings.LastBackup(ctx); err != nil || !at.IsZero() || was != "" {
		t.Fatalf("before any backup: %v %q %v", at, was, err)
	}
	when := time.Date(2026, 9, 30, 3, 0, 0, 123, time.UTC)
	if err := settings.SetLastBackup(ctx, when, "@daily"); err != nil {
		t.Fatal(err)
	}
	cur, _ := settings.Get(ctx)
	if _, err := settings.Save(ctx, cur); err != nil {
		t.Fatal(err)
	}
	at, was, err := NewSettings(db, "en").LastBackup(ctx)
	if err != nil || !at.Equal(when) || was != "@daily" {
		t.Errorf("read back: %v %q %v, want %v @daily", at, was, err, when)
	}
	if err := settings.SetLastBackup(ctx, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if at, was, _ := settings.LastBackup(ctx); !at.IsZero() || was != "" {
		t.Errorf("cleared, it still says %v %q", at, was)
	}
}

// The report goes as Markdown: a customer named with an underscore made
// Telegram refuse the whole of it, on every schedule, until they were gone.
func TestTheReportKeepsNamesFromBeingMarkdown(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	soon := time.Now().UTC().Add(24 * time.Hour)
	for _, c := range []model.Client{
		{Name: "ali_reza", Protocol: model.ProtocolWireGuard, Status: model.StatusActive, ExpiresAt: &soon},
		{Name: "*vip*", Protocol: model.ProtocolWireGuard, Status: model.StatusActive, QuotaBytes: 100, UsedBytes: 90},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := NewReporter(db, NewClients(db, nil, quietLog()), NewSettings(db, "en"), nil, "test")
	text, err := r.Build(ctx, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"ali_reza", "*vip*"} {
		if strings.Contains(text, raw) {
			t.Errorf("the report carries %q as it is:\n%s", raw, text)
		}
	}
	if !strings.Contains(text, "ali reza") || !strings.Contains(text, " vip ") {
		t.Errorf("the customers are not in the report:\n%s", text)
	}
}
