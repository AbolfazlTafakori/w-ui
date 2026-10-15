package ikev2driver

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestTheDeviceIsReadFromIP(t *testing.T) {
	xfrm := `[{"ifname":"ike0","linkinfo":{"info_kind":"xfrm","info_data":{"link":"lo","if_id":1465188355}}}]`
	if k, id := parseLinkInfo([]byte(xfrm)); k != "xfrm" || id != 1465188355 {
		t.Fatalf("%s %d", k, id)
	}
	// As Ubuntu 24.04's ip prints it.
	hex := `[{"ifname":"ike0","linkinfo":{"info_kind":"xfrm","info_data":{"if_id":"0x57550007"}}}]`
	if k, id := parseLinkInfo([]byte(hex)); k != "xfrm" || id != 0x57550007 {
		t.Fatalf("hex: %s %#x", k, id)
	}
	if k, _ := parseLinkInfo([]byte(`[{"ifname":"ike0","linkinfo":{"info_kind":"dummy"}}]`)); k != "dummy" {
		t.Fatal(k)
	}
	if k, _ := parseLinkInfo([]byte(`[{"ifname":"eth0"}]`)); k != "other" {
		t.Fatalf("a device without link info is %q", k)
	}
	if k, _ := parseLinkInfo([]byte(`[]`)); k != "" {
		t.Fatal(k)
	}
	if IfID(&model.Interface{ID: 3}) != 0x57550003 {
		t.Fatal("if_id")
	}
}
