package reconciler

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// writeRowByRow is how a flush was written before writeFlush: a statement a
// row, through the model. Kept here as what writeFlush must equal.
func writeRowByRow(tx *gorm.DB, usage, devices map[uint]usageDelta, liveness map[uint]trafficUpdate,
	owners map[uint]uint, bucket time.Time) error {
	byOwner := map[uint]uint64{}
	for id, d := range usage {
		if owner := owners[id]; owner != 0 {
			byOwner[owner] += d.Bytes
		}
		cols := map[string]any{"used_bytes": gorm.Expr("used_bytes + ?", d.Bytes)}
		if d.Up > 0 {
			cols["up_bytes"] = gorm.Expr("up_bytes + ?", d.Up)
		}
		if d.Down > 0 {
			cols["down_bytes"] = gorm.Expr("down_bytes + ?", d.Down)
		}
		if err := tx.Model(&model.Client{}).Where("id = ?", id).UpdateColumns(cols).Error; err != nil {
			return err
		}
		rx, tx2 := d.Down, d.Up
		var sample model.TrafficSample
		err := tx.Where("client_id = ? AND bucket_ts = ? AND granularity = ?",
			id, bucket, model.GranularityFine).First(&sample).Error
		if err == nil {
			if err := tx.Model(&sample).UpdateColumns(map[string]any{
				"rx": gorm.Expr("rx + ?", rx), "tx": gorm.Expr("tx + ?", tx2),
			}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&model.TrafficSample{ClientID: id, BucketTS: bucket,
			Granularity: model.GranularityFine, RX: rx, TX: tx2}).Error; err != nil {
			return err
		}
	}
	for accountID, d := range devices {
		if err := tx.Model(&model.Account{}).Where("id = ?", accountID).UpdateColumns(map[string]any{
			"up_bytes": gorm.Expr("up_bytes + ?", d.Up), "down_bytes": gorm.Expr("down_bytes + ?", d.Down),
		}).Error; err != nil {
			return err
		}
	}
	for accountID, u := range liveness {
		fields := map[string]any{"last_seen_at": u.At}
		if !u.Handshake.IsZero() {
			fields["last_handshake"] = u.Handshake
		}
		if u.Endpoint != "" {
			fields["last_endpoint"] = u.Endpoint
		}
		if err := tx.Model(&model.Account{}).Where("id = ?", accountID).Updates(fields).Error; err != nil {
			return err
		}
	}
	for ownerID, delta := range byOwner {
		if err := tx.Model(&model.Admin{}).Where("id = ?", ownerID).
			UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", delta)).Error; err != nil {
			return err
		}
	}
	return nil
}

// seedFlushDB gives two databases the same customers, files, resellers and
// earlier samples.
func seedFlushDB(t *testing.T, db *gorm.DB, customers int) {
	t.Helper()
	for _, a := range []model.Admin{{Username: "r1", PasswordHash: "x", Role: model.RoleReseller}, {Username: "r2", PasswordHash: "x", Role: model.RoleReseller}} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	iface := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.70.0.0/16", NodeID: 1}
	if err := db.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	n := 0 // files made so far, for their addresses
	for i := 1; i <= customers; i++ {
		c := model.Client{Name: fmt.Sprintf("c%d", i), OwnerID: uint(i % 3), SubToken: fmt.Sprintf("tok-%d", i),
			UsedBytes: uint64(i) * 1000, UpBytes: uint64(i) * 300, DownBytes: uint64(i) * 700}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		// Rolled-up samples in the same bucket as the first flushes': a
		// look for this bucket's fine sample finds them too, and must pass
		// them by.
		if i%7 == 0 {
			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			for _, g := range []model.Granularity{model.GranularityHourly, model.GranularityDaily} {
				if err := db.Create(&model.TrafficSample{ClientID: c.ID, BucketTS: at, Granularity: g, RX: 5, TX: 6}).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
		for f := 0; f < 1+i%3; f++ {
			n++
			if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: iface.ID, NodeID: 1,
				DeviceName: fmt.Sprintf("d%d", f), IP: fmt.Sprintf("10.70.%d.%d", n/250, n%250+2),
				LastEndpoint: "198.51.100.1:5000"}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

// flushes is the same sequence of flushes, from one seed, for both writers.
func flushes(seed int64, customers, accounts int) []func(w func(*gorm.DB, map[uint]usageDelta, map[uint]usageDelta, map[uint]trafficUpdate, map[uint]uint, time.Time) error, db *gorm.DB) error {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var out []func(w func(*gorm.DB, map[uint]usageDelta, map[uint]usageDelta, map[uint]trafficUpdate, map[uint]uint, time.Time) error, db *gorm.DB) error
	for f := 0; f < 6; f++ {
		usage, devices := map[uint]usageDelta{}, map[uint]usageDelta{}
		liveness := map[uint]trafficUpdate{}
		owners := map[uint]uint{}
		for id := 1; id <= customers; id++ {
			if rng.Intn(3) == 0 {
				continue
			}
			up, down := uint64(rng.Intn(1<<20)), uint64(rng.Intn(1<<20))
			if rng.Intn(5) == 0 {
				up = 0 // a customer whose direction was not split
			}
			usage[uint(id)] = usageDelta{Bytes: up + down, Up: up, Down: down}
			if id%3 != 0 {
				owners[uint(id)] = uint(id % 3)
			}
		}
		for id := 1; id <= accounts; id++ {
			if rng.Intn(2) == 0 {
				devices[uint(id)] = usageDelta{Up: uint64(rng.Intn(1 << 16)), Down: uint64(rng.Intn(1 << 16))}
			}
			if rng.Intn(3) == 0 {
				u := trafficUpdate{AccountID: uint(id), At: base.Add(time.Duration(f) * time.Minute)}
				if rng.Intn(2) == 0 {
					u.Handshake = base.Add(time.Duration(f)*time.Minute - time.Second)
				}
				if rng.Intn(2) == 0 {
					u.Endpoint = fmt.Sprintf("203.0.113.%d:%d", rng.Intn(250), 1000+rng.Intn(9000))
				}
				liveness[uint(id)] = u
			}
		}
		// Two flushes share a bucket, so the second finds the first's samples.
		bucket := base.Add(time.Duration(f/2) * 5 * time.Minute)
		out = append(out, func(w func(*gorm.DB, map[uint]usageDelta, map[uint]usageDelta, map[uint]trafficUpdate, map[uint]uint, time.Time) error, db *gorm.DB) error {
			return db.Transaction(func(tx *gorm.DB) error { return w(tx, usage, devices, liveness, owners, bucket) })
		})
	}
	return out
}

// dumpFlushDB is every column a flush writes, in order: what two databases
// written by the two writers must agree on to the byte.
func dumpFlushDB(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var b strings.Builder
	var clients []model.Client
	db.Order("id").Find(&clients)
	for _, c := range clients {
		fmt.Fprintf(&b, "client %d used=%d up=%d down=%d\n", c.ID, c.UsedBytes, c.UpBytes, c.DownBytes)
	}
	var accounts []model.Account
	db.Order("id").Find(&accounts)
	for _, a := range accounts {
		hs, seen := "-", "-"
		if a.LastHandshake != nil {
			hs = a.LastHandshake.UTC().Format(time.RFC3339Nano)
		}
		if a.LastSeenAt != nil {
			seen = a.LastSeenAt.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&b, "account %d up=%d down=%d hs=%s seen=%s ep=%s\n", a.ID, a.UpBytes, a.DownBytes, hs, seen, a.LastEndpoint)
	}
	var samples []model.TrafficSample
	db.Find(&samples)
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].ClientID != samples[j].ClientID {
			return samples[i].ClientID < samples[j].ClientID
		}
		if !samples[i].BucketTS.Equal(samples[j].BucketTS) {
			return samples[i].BucketTS.Before(samples[j].BucketTS)
		}
		return samples[i].Granularity < samples[j].Granularity
	})
	for _, s := range samples {
		fmt.Fprintf(&b, "sample client=%d bucket=%s %s rx=%d tx=%d\n", s.ClientID, s.BucketTS.UTC().Format(time.RFC3339), s.Granularity, s.RX, s.TX)
	}
	var admins []model.Admin
	db.Order("id").Find(&admins)
	for _, a := range admins {
		fmt.Fprintf(&b, "admin %d used=%d\n", a.ID, a.UsedBytes)
	}
	return b.String()
}

func compareWriters(t *testing.T, open func(t *testing.T, name string) *gorm.DB) {
	const customers = 900 // past two chunks of 400
	before, after := open(t, "rowbyrow"), open(t, "batched")
	seedFlushDB(t, before, customers)
	seedFlushDB(t, after, customers)
	var accounts int64
	before.Model(&model.Account{}).Count(&accounts)
	for i, f := range flushes(42, customers, int(accounts)) {
		if err := f(writeRowByRow, before); err != nil {
			t.Fatalf("flush %d, row by row: %v", i, err)
		}
		if err := f(writeFlush, after); err != nil {
			t.Fatalf("flush %d, batched: %v", i, err)
		}
	}
	want, got := dumpFlushDB(t, before), dumpFlushDB(t, after)
	if want != got {
		wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
		for i := range wl {
			if i >= len(gl) || wl[i] != gl[i] {
				g := "(missing)"
				if i < len(gl) {
					g = gl[i]
				}
				t.Fatalf("the batched writer differs at line %d:\n want %s\n got  %s", i+1, wl[i], g)
			}
		}
		t.Fatalf("the batched writer wrote %d lines, row by row %d", len(gl), len(wl))
	}
	if !strings.Contains(want, "sample client=1 ") || !strings.Contains(want, "hs=2026") {
		t.Fatal("the comparison saw no samples or no handshakes; it compared nothing")
	}
}

// The batched writer leaves the database exactly as writing a row at a
// time did: every customer, file, sample and reseller, to the byte.
func TestTheBatchedWriterWritesWhatRowByRowDid(t *testing.T) {
	compareWriters(t, func(t *testing.T, name string) *gorm.DB {
		t.Helper()
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%s?mode=memory&cache=shared",
			strings.ReplaceAll(t.Name(), "/", "_"), name)), &gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Migrate(db); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if s, err := db.DB(); err == nil {
				_ = s.Close()
			}
		})
		return db
	})
}

// And on PostgreSQL, where CI offers one.
func TestTheBatchedWriterWritesWhatRowByRowDidOnPostgres(t *testing.T) {
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	compareWriters(t, func(t *testing.T, name string) *gorm.DB {
		t.Helper()
		schema := fmt.Sprintf("flush_%s_%d", name, time.Now().UnixNano())
		admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE") })
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		db, err := gorm.Open(postgres.Open(dsn+sep+"search_path="+schema), &gorm.Config{Logger: gormlogger.Discard})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Migrate(db); err != nil {
			t.Fatal(err)
		}
		return db
	})
}
