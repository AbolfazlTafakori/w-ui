package backup

import (
	"encoding/json"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func setting(t *testing.T, db *gorm.DB, key string) (string, bool) {
	t.Helper()
	var s model.Setting
	err := db.Where("key = ?", key).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s.Value, true
}

func put(t *testing.T, db *gorm.DB, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := db.Save(&model.Setting{Key: k, Value: v}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// through carries a stash across the restart the way a restore does: written
// to JSON beside the staged files, read back on the next start.
func through(t *testing.T, a *LocalAddresses) *LocalAddresses {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back LocalAddresses
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	return &back
}

// An archive from another server, or from this one before its port or URL path
// changed, carries its own. Put back as they were, the panel came up on a port
// the firewall never opened, under a path nobody had -- or, with a certificate
// path that does not exist here, not at all. Whichever way the addresses are
// kept, how the panel itself is reached is this server's.
func TestARestoreNeverChangesHowThePanelIsReached(t *testing.T) {
	for _, keepAddresses := range []bool{true, false} {
		here := addrDB(t)
		put(t, here, map[string]string{
			"panel.port":     "2096",
			"panel.basePath": "/new-secret/",
			"panel.certFile": "/etc/wui/cert/fullchain.pem",
			"panel.keyFile":  "/etc/wui/cert/privkey.pem",
			"sub.port":       "2097",
			// Not how the panel is reached: an ordinary setting, which the
			// archive's value replaces.
			"panel.pageSize": "25",
		})
		stash, err := ReadLocalAddresses(here, keepAddresses)
		if err != nil {
			t.Fatal(err)
		}

		restored := addrDB(t)
		put(t, restored, map[string]string{
			"panel.port":     "54321",
			"panel.basePath": "/old-server-path/",
			"panel.certFile": "/root/cert/old-server.example.com/fullchain.pem",
			"panel.keyFile":  "/root/cert/old-server.example.com/privkey.pem",
			// Set on the old server, left to the environment on this one.
			"panel.listen":   "10.0.0.5",
			"sub.port":       "8443",
			"panel.pageSize": "100",
		})

		if _, _, err := ApplyLocalAddresses(restored, through(t, stash)); err != nil {
			t.Fatalf("keepAddresses=%v: %v", keepAddresses, err)
		}
		for key, want := range map[string]string{
			"panel.port":     "2096",
			"panel.basePath": "/new-secret/",
			"panel.certFile": "/etc/wui/cert/fullchain.pem",
			"panel.keyFile":  "/etc/wui/cert/privkey.pem",
			"sub.port":       "2097",
			"panel.pageSize": "100",
		} {
			if got, _ := setting(t, restored, key); got != want {
				t.Errorf("keepAddresses=%v: %s = %q, want %q", keepAddresses, key, got, want)
			}
		}
		if got, has := setting(t, restored, "panel.listen"); has {
			t.Errorf("keepAddresses=%v: the old server's listen address %q was kept; this server leaves it to its environment",
				keepAddresses, got)
		}
	}
}

// Taking the archive's addresses (cloning a server, or restoring onto the one it
// came from) leaves the customer-facing ones as the archive has them.
func TestTakingTheArchivesAddressesKeepsThem(t *testing.T) {
	here := addrDB(t)
	iface := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.66.0.0/16", EndpointHost: "here.example.com", MTU: 1420,
	}
	if err := here.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	put(t, here, map[string]string{"sub.host": "sub.here.example.com"})
	stash, err := ReadLocalAddresses(here, false)
	if err != nil {
		t.Fatal(err)
	}

	restored := addrDB(t)
	old := iface
	old.ID = 0
	old.EndpointHost = "archive.example.com"
	if err := restored.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	put(t, restored, map[string]string{"sub.host": "sub.archive.example.com"})

	if _, _, err := ApplyLocalAddresses(restored, through(t, stash)); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	restored.First(&got, old.ID)
	if got.EndpointHost != "archive.example.com" {
		t.Errorf("endpoint = %q, want the archive's", got.EndpointHost)
	}
	if v, _ := setting(t, restored, "sub.host"); v != "sub.archive.example.com" {
		t.Errorf("sub.host = %q, want the archive's", v)
	}
}

// A restore staged by the panel before this existed carries no Access and no
// flag: it was written only to keep the addresses, and is applied that way,
// without touching settings it says nothing about.
func TestAStashFromBeforeKeepsItsMeaning(t *testing.T) {
	old := []byte(`{"interfaces":{"wg0":"here.example.com"},"hosts":{},"settings":{},"localNodeAddress":""}`)
	var a LocalAddresses
	if err := json.Unmarshal(old, &a); err != nil {
		t.Fatal(err)
	}
	if !a.keepsAddresses() {
		t.Error("an old stash was read as not keeping the addresses")
	}
	restored := addrDB(t)
	put(t, restored, map[string]string{"panel.port": "54321"})
	if _, _, err := ApplyLocalAddresses(restored, &a); err != nil {
		t.Fatal(err)
	}
	if v, _ := setting(t, restored, "panel.port"); v != "54321" {
		t.Errorf("an old stash changed panel.port to %q", v)
	}
}
