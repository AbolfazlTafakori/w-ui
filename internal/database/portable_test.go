package database

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

func memDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// The whole point: secrets, JSON columns, times and booleans survive the trip,
// into a database that is not the one they came from.
func TestPortableRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := memDB(t)

	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	iface := model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16",
		PrivateKey: "iface-secret", AWG: model.JSON(model.AWGParams{Jc: 4, S1: 10})}
	if err := src.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	client := model.Client{Name: "alice", QuotaBytes: 5 << 30, ExpiresAt: &exp, SubToken: "tok-secret"}
	if err := src.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	acct := model.Account{ClientID: client.ID, InterfaceID: iface.ID, PublicKey: "pk", PrivateKey: "acct-secret", IP: "10.66.0.2", DeviceName: "phone"}
	if err := src.Create(&acct).Error; err != nil {
		t.Fatal(err)
	}
	admin := model.Admin{Username: "root", PasswordHash: "$2a$10$hash", TOTPSecret: "totp", SessionEpoch: 3}
	if err := src.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := src.Create(&model.Setting{Key: "sub.enabled", Value: "true"}).Error; err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Export(ctx, src, config.DriverSQLite, "test", &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("acct-secret")) || !bytes.Contains(buf.Bytes(), []byte("$2a$10$hash")) {
		t.Fatal("the dump left the secrets out")
	}

	dst := memDB(t)
	// Something already there, which must be gone afterwards.
	if err := dst.Create(&model.Client{Name: "stale"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, dst, config.DriverSQLite, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatal(err)
	}

	var gotIface model.Interface
	if err := dst.First(&gotIface, iface.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotIface.PrivateKey != "iface-secret" || gotIface.AWG.V.Jc != 4 || gotIface.AWG.V.S1 != 10 {
		t.Errorf("interface came back wrong: %+v", gotIface)
	}
	var clients []model.Client
	dst.Find(&clients)
	if len(clients) != 1 || clients[0].Name != "alice" || clients[0].SubToken != "tok-secret" {
		t.Fatalf("clients: %+v", clients)
	}
	if clients[0].ExpiresAt == nil || !clients[0].ExpiresAt.Equal(exp) || clients[0].QuotaBytes != 5<<30 {
		t.Errorf("client fields: %+v", clients[0])
	}
	var gotAcct model.Account
	if err := dst.First(&gotAcct, acct.ID).Error; err != nil || gotAcct.PrivateKey != "acct-secret" || gotAcct.ClientID != client.ID {
		t.Errorf("account: %+v %v", gotAcct, err)
	}
	var gotAdmin model.Admin
	if err := dst.First(&gotAdmin, admin.ID).Error; err != nil || gotAdmin.PasswordHash != "$2a$10$hash" || gotAdmin.TOTPSecret != "totp" || gotAdmin.SessionEpoch != 3 {
		t.Errorf("admin: %+v %v", gotAdmin, err)
	}
	var s model.Setting
	if err := dst.First(&s, "key = ?", "sub.enabled").Error; err != nil || s.Value != "true" {
		t.Errorf("setting: %+v %v", s, err)
	}

	// Ids are kept, and the next insert does not collide with them.
	next := model.Client{Name: "bob"}
	if err := dst.Create(&next).Error; err != nil {
		t.Fatal(err)
	}
	if next.ID <= client.ID {
		t.Errorf("new id %d not past the restored %d", next.ID, client.ID)
	}
}

func TestImportRefusesNewerFormat(t *testing.T) {
	db := memDB(t)
	err := Import(context.Background(), db, config.DriverSQLite, bytes.NewReader([]byte(`{"format":99,"tables":{}}`)), nil)
	if err == nil {
		t.Fatal("a newer format was accepted")
	}
}

func TestImportIgnoresUnknownColumns(t *testing.T) {
	db := memDB(t)
	dump := `{"format":1,"tables":{"clients":[{"id":7,"name":"x","future_column":"whatever","enabled":1}]}}`
	if err := Import(context.Background(), db, config.DriverSQLite, bytes.NewReader([]byte(dump)), nil); err != nil {
		t.Fatal(err)
	}
	var c model.Client
	if err := db.First(&c, 7).Error; err != nil || c.Name != "x" {
		t.Fatalf("%+v %v", c, err)
	}
}

// The cross-engine direction that matters: a SQLite dump into PostgreSQL.
// Runs where a server is offered (CI starts one); skipped otherwise.
func TestPortableIntoPostgres(t *testing.T) {
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	src := memDB(t)
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	iface := model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16", PrivateKey: "s", AWG: model.JSON(model.AWGParams{Jc: 4})}
	if err := src.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	client := model.Client{Name: "alice", ExpiresAt: &exp, SubToken: "tok", StartOnFirstUse: true, Status: "disabled"}
	if err := src.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	if err := src.Create(&model.Account{ClientID: client.ID, InterfaceID: iface.ID, IP: "10.66.0.2", DeviceName: "d", PrivateKey: "k"}).Error; err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Export(ctx, src, config.DriverSQLite, "test", &buf); err != nil {
		t.Fatal(err)
	}

	pg, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(pg); err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, pg, config.DriverPostgres, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatal(err)
	}
	var got model.Client
	if err := pg.First(&got, client.ID).Error; err != nil || !got.StartOnFirstUse || got.Status != "disabled" || got.SubToken != "tok" || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("%+v %v", got, err)
	}
	var gi model.Interface
	if err := pg.First(&gi, iface.ID).Error; err != nil || gi.AWG.V.Jc != 4 || gi.PrivateKey != "s" {
		t.Fatalf("%+v %v", gi, err)
	}
	next := model.Client{Name: "bob"}
	if err := pg.Create(&next).Error; err != nil {
		t.Fatalf("insert after import: %v", err)
	}

	// And back out again, into SQLite.
	buf.Reset()
	if err := Export(ctx, pg, config.DriverPostgres, "test", &buf); err != nil {
		t.Fatal(err)
	}
	back := memDB(t)
	if err := Import(ctx, back, config.DriverSQLite, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatal(err)
	}
	var n int64
	back.Model(&model.Client{}).Count(&n)
	if n != 2 {
		t.Fatalf("clients back in sqlite: %d", n)
	}
}
