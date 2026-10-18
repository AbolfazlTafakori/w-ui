package oversight

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/panelconf"
	"github.com/abolfazl/w-ui/internal/service"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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
	r := NewReporter(db, service.NewClients(db, nil, quietLog()), panelconf.NewSettings(db, "en"), nil, "test")
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
