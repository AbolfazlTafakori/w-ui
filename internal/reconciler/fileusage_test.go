package reconciler

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// The rule handed to the kernel names every file with its account, so the
// kernel can count each one on its own.
func TestTheRuleNamesEveryFile(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", DeviceLimit: 2}, "10.66.0.2")
	second := model.Account{ClientID: id, InterfaceID: 1, NodeID: 1, DeviceName: "device-2",
		IP: "10.66.0.3", PublicKey: "pk-2", Enabled: true}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}

	r.Tick(context.Background())

	rule, _ := enf.ruleFor(enforce.Key(id))
	var accs []model.Account
	db.Where("client_id = ?", id).Order("id").Find(&accs)
	if len(rule.Files) != 2 || rule.Files[0].Account != accs[0].ID || rule.Files[1].Account != accs[1].ID ||
		rule.Files[0].Addr.String() != "10.66.0.2" || rule.Files[1].Addr.String() != "10.66.0.3" {
		t.Fatalf("rule files = %+v, accounts %d %d", rule.Files, accs[0].ID, accs[1].ID)
	}
}

// What the kernel counted on each file is what each file's row holds, and the
// rows add up to the customer's usage to the byte -- the per-user table is a
// measurement, not a share worked out from the total.
func TestEachFileIsChargedWhatTheKernelCountedOnIt(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", DeviceLimit: 2}, "10.66.0.2")
	second := model.Account{ClientID: id, InterfaceID: 1, NodeID: 1, DeviceName: "device-2",
		IP: "10.66.0.3", PublicKey: "pk-2", Enabled: true}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	var first model.Account
	db.Where("client_id = ? AND ip = ?", id, "10.66.0.2").First(&first)

	for tick := 0; tick < 5; tick++ {
		// Ticks on which only one file moves, the case the old estimate lost.
		files := []enforce.FileUsage{{Account: first.ID, Up: 1000, Down: 9000}}
		if tick%2 == 0 {
			files = append(files, enforce.FileUsage{Account: second.ID, Up: 7, Down: 93})
		}
		var u enforce.Usage
		u.Key = enforce.Key(id)
		u.Files = files
		for _, f := range files {
			u.Up += f.Up
			u.Down += f.Down
		}
		u.Bytes = u.Up + u.Down
		enf.drain = []enforce.Usage{u}
		r.Tick(context.Background())
	}
	r.writer.flush(context.Background())

	var c model.Client
	db.First(&c, id)
	var a1, a2 model.Account
	db.First(&a1, first.ID)
	db.First(&a2, second.ID)
	if a1.UpBytes != 5000 || a1.DownBytes != 45000 {
		t.Fatalf("first file: up %d down %d, want 5000 45000", a1.UpBytes, a1.DownBytes)
	}
	if a2.UpBytes != 21 || a2.DownBytes != 279 {
		t.Fatalf("second file: up %d down %d, want 21 279", a2.UpBytes, a2.DownBytes)
	}
	if sum := a1.UpBytes + a1.DownBytes + a2.UpBytes + a2.DownBytes; sum != c.UsedBytes {
		t.Fatalf("the files add up to %d, the customer used %d", sum, c.UsedBytes)
	}
	if c.UpBytes != a1.UpBytes+a2.UpBytes || c.DownBytes != a1.DownBytes+a2.DownBytes {
		t.Fatalf("directions disagree: customer up %d down %d", c.UpBytes, c.DownBytes)
	}
}

// Counters an older panel left in the kernel count whole clients. Their bytes
// are billed to the client and to no file: nothing is guessed.
func TestAWholeClientCountChargesNoFile(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali"}, "10.66.0.2")

	enf.drain = []enforce.Usage{{Key: enforce.Key(id), Bytes: 500, Up: 100, Down: 400}}
	r.Tick(context.Background())
	r.writer.flush(context.Background())

	var c model.Client
	db.First(&c, id)
	var a model.Account
	db.Where("client_id = ?", id).First(&a)
	if c.UsedBytes != 500 || a.UpBytes+a.DownBytes != 0 {
		t.Fatalf("customer %d, file %d; want 500 and nothing", c.UsedBytes, a.UpBytes+a.DownBytes)
	}
}

// However many updates arrive between two flushes -- far more than any queue
// would have held while the database was busy -- every byte is written.
func TestTheWriterNeverDropsAnUpdate(t *testing.T) {
	db := newTestDB(t)
	c := model.Client{Name: "Ali", Status: model.StatusActive, DeviceLimit: 1}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	a := model.Account{ClientID: c.ID, InterfaceID: 1, NodeID: 1, DeviceName: "d", IP: "10.66.0.2", Enabled: true}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	w := newTrafficWriter(db, quietLog())
	const n = 20000
	for i := 0; i < n; i++ {
		w.submit(
			trafficUpdate{Key: enforce.Key(c.ID), Bytes: 3, Up: 1, Down: 2},
			trafficUpdate{AccountID: a.ID, DevUp: 1, DevDown: 2},
		)
	}
	w.flush(context.Background())

	db.First(&c, c.ID)
	db.First(&a, a.ID)
	if c.UsedBytes != 3*n || a.UpBytes+a.DownBytes != 3*n {
		t.Fatalf("customer %d, file %d; want %d each", c.UsedBytes, a.UpBytes+a.DownBytes, 3*n)
	}
}

// A flush the database refuses writes nothing and loses nothing: what it
// carried is written by the next flush that succeeds.
func TestAFailedFlushIsWrittenByTheNext(t *testing.T) {
	db := newTestDB(t)
	c := model.Client{Name: "Ali", Status: model.StatusActive, DeviceLimit: 1}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	a := model.Account{ClientID: c.ID, InterfaceID: 1, NodeID: 1, DeviceName: "d", IP: "10.66.0.2", Enabled: true}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	w := newTrafficWriter(db, quietLog())
	w.submit(trafficUpdate{Key: enforce.Key(c.ID), Bytes: 300, Up: 100, Down: 200},
		trafficUpdate{AccountID: a.ID, DevUp: 100, DevDown: 200})

	// The history table gone makes the flush's transaction fail part way.
	if err := db.Migrator().RenameTable("traffic_samples", "traffic_samples_away"); err != nil {
		t.Fatal(err)
	}
	w.flush(context.Background())
	db.First(&c, c.ID)
	if c.UsedBytes != 0 {
		t.Fatalf("a failed flush left %d bytes behind", c.UsedBytes)
	}

	if err := db.Migrator().RenameTable("traffic_samples_away", "traffic_samples"); err != nil {
		t.Fatal(err)
	}
	w.submit(trafficUpdate{Key: enforce.Key(c.ID), Bytes: 30, Up: 10, Down: 20},
		trafficUpdate{AccountID: a.ID, DevUp: 10, DevDown: 20})
	w.flush(context.Background())

	db.First(&c, c.ID)
	db.First(&a, a.ID)
	if c.UsedBytes != 330 || c.UpBytes != 110 || c.DownBytes != 220 {
		t.Fatalf("customer = used %d up %d down %d, want 330 110 220", c.UsedBytes, c.UpBytes, c.DownBytes)
	}
	if a.UpBytes != 110 || a.DownBytes != 220 {
		t.Fatalf("file = up %d down %d, want 110 220", a.UpBytes, a.DownBytes)
	}
}
