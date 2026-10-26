package reconciler

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A tunnel's other addresses reach its driver: an L2TP tunnel behind a
// relay has to listen for the relay, and an IKEv2 certificate names them.
func TestATunnelIsGivenItsOtherAddresses(t *testing.T) {
	r, db, _, _ := newRig(t)
	for _, h := range []model.Host{
		{InterfaceID: 1, Name: "relay", Address: "relay.example.ir", Enabled: true, Priority: 1},
		{InterfaceID: 1, Name: "off", Address: "off.example", Enabled: false},
		{InterfaceID: 2, Name: "other", Address: "other.example", Enabled: true},
	} {
		if err := db.Create(&h).Error; err != nil {
			t.Fatal(err)
		}
	}
	// false is the column's zero, which a create leaves to the default.
	db.Model(&model.Host{}).Where("name = ?", "off").Update("enabled", false)
	d, err := r.readDesired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.interfaces) != 1 {
		t.Fatalf("%d interfaces", len(d.interfaces))
	}
	got := d.interfaces[0].Hosts
	if len(got) != 1 || got[0].Address != "relay.example.ir" {
		t.Fatalf("hosts given to the driver: %+v", got)
	}
}
