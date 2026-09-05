package backup

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func addrDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	return db
}

// The reason to restore onto another machine is usually that the first one was
// blocked or lost. Restoring its address faithfully hands every customer a
// configuration pointing at the server that stopped working, which is restoring
// the problem along with the data.
func TestThisServersAddressSurvivesARestoreFromAnother(t *testing.T) {
	// What this machine says about itself, read before the restore.
	here := addrDB(t)
	iface := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.66.0.0/16", EndpointHost: "new-server.example.com", MTU: 1420,
	}
	if err := here.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	if err := here.Create(&model.Host{
		InterfaceID: iface.ID, Name: "spare", Address: "new-spare.example.com", Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	kept, err := ReadLocalAddresses(here)
	if err != nil {
		t.Fatalf("ReadLocalAddresses: %v", err)
	}

	// The restored database, which came from the old server.
	restored := addrDB(t)
	old := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.66.0.0/16", EndpointHost: "old-blocked-server.example.com", MTU: 1420,
	}
	if err := restored.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := restored.Create(&model.Host{
		InterfaceID: old.ID, Name: "spare", Address: "old-spare.example.com", Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	n, unknown, err := ApplyLocalAddresses(restored, kept)
	if err != nil {
		t.Fatalf("ApplyLocalAddresses: %v", err)
	}
	if n != 1 || unknown != 0 {
		t.Errorf("kept %d and did not recognise %d, want 1 and 0", n, unknown)
	}

	var got model.Interface
	if err := restored.First(&got, old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.EndpointHost != "new-server.example.com" {
		t.Errorf("customers would be sent to %q, which is the server the backup came from", got.EndpointHost)
	}

	var hosts []model.Host
	if err := restored.Find(&hosts).Error; err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Address != "new-spare.example.com" {
		t.Errorf("the spare addresses are still the old server's: %+v", hosts)
	}
}

// A tunnel in the archive that this machine never had has no local address to
// put back. Blanking it would leave a customer with a configuration naming
// nowhere at all, so it keeps what it has and is counted so it can be said.
func TestATunnelThisServerNeverHadKeepsItsAddress(t *testing.T) {
	here := addrDB(t)
	kept, err := ReadLocalAddresses(here)
	if err != nil {
		t.Fatal(err)
	}

	restored := addrDB(t)
	if err := restored.Create(&model.Interface{
		Name: "wg9", Protocol: model.ProtocolWireGuard, ListenPort: 51821,
		Subnet: "10.70.0.0/16", EndpointHost: "old.example.com", MTU: 1420,
	}).Error; err != nil {
		t.Fatal(err)
	}

	n, unknown, err := ApplyLocalAddresses(restored, kept)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || unknown != 1 {
		t.Errorf("kept %d and did not recognise %d, want 0 and 1", n, unknown)
	}

	var got model.Interface
	if err := restored.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.EndpointHost != "old.example.com" {
		t.Errorf("a tunnel with no local counterpart was left pointing at %q", got.EndpointHost)
	}
}

// Turning it off is how one machine is cloned onto another on purpose, and then
// the archive's addresses are what is wanted.
func TestCloningTakesTheArchivesAddresses(t *testing.T) {
	restored := addrDB(t)
	if err := restored.Create(&model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.66.0.0/16", EndpointHost: "source.example.com", MTU: 1420,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if _, _, err := ApplyLocalAddresses(restored, nil); err != nil {
		t.Fatalf("ApplyLocalAddresses(nil): %v", err)
	}

	var got model.Interface
	if err := restored.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.EndpointHost != "source.example.com" {
		t.Errorf("a deliberate clone had its address changed to %q", got.EndpointHost)
	}
}
