package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A WireGuard customer on an interface with hosts is handed one config per
// host, named after it, at the host's address; the interface's own address
// is not handed out on its own. No hosts: the interface's own, once. A
// blank-address host inherits the interface's address, and a host left out
// of a format is left out.
func TestVariantsFanOutPerHost(t *testing.T) {
	iface := &model.Interface{Protocol: model.ProtocolWireGuard, EndpointHost: "vpn.example.com", ListenPort: 51820}
	if got := variantsFor(iface, "conf"); len(got) != 1 || got[0].Host != nil || got[0].Endpoint != "vpn.example.com" || got[0].Port != 51820 {
		t.Fatalf("no hosts: got %+v", got)
	}
	iface.Hosts = []model.Host{
		{ID: 1, Name: "cdn", Address: "cdn.example.com", Port: 443, Enabled: true, Priority: 2},
		{ID: 2, Name: "raw", Address: "", Enabled: true, Priority: 1},
		{ID: 3, Name: "off", Address: "off.example.com", Enabled: false},
		{ID: 4, Name: "nozip", Address: "z.example.com", Enabled: true, Priority: 3, ExcludeFormats: "zip"},
	}
	got := variantsFor(iface, "conf")
	if len(got) != 3 {
		t.Fatalf("want 3 variants, got %d", len(got))
	}
	if got[0].Host.Name != "raw" || got[0].Endpoint != "vpn.example.com" || got[0].Port != 51820 {
		t.Errorf("first should be the inheriting host at the interface's address: %+v", got[0])
	}
	if got[1].Host.Name != "cdn" || got[1].Endpoint != "cdn.example.com" || got[1].Port != 443 {
		t.Errorf("second should be cdn:443: %+v", got[1])
	}
	if zipped := variantsFor(iface, "zip"); len(zipped) != 2 {
		t.Errorf("the host excluded from zip should be left out: %d", len(zipped))
	}
	if name := variantFilename("phone.conf", got[1]); name != "phone-cdn.conf" {
		t.Errorf("filename = %q", name)
	}
	ovpn := &model.Interface{Protocol: model.ProtocolOpenVPN, EndpointHost: "vpn.example.com", ListenPort: 1194, Hosts: iface.Hosts}
	if got := variantsFor(ovpn, ""); len(got) != 1 {
		t.Errorf("OpenVPN carries its hosts as remotes inside one profile, not as variants: %d", len(got))
	}
}

func TestGroupHostsRegroupsRows(t *testing.T) {
	rows := []model.Host{
		{ID: 1, GroupID: "g", InterfaceID: 1, Name: "cdn", Address: "a.example.com", Port: 0, Priority: 2, Enabled: true, Reachable: true},
		{ID: 2, GroupID: "g", InterfaceID: 2, Name: "cdn", Address: "a.example.com", Port: 0, Priority: 2, Enabled: true, Reachable: false, LastError: "timeout"},
		{ID: 3, GroupID: "g", InterfaceID: 1, Name: "cdn", Address: "b.example.com", Port: 443, Priority: 2, Enabled: true, Reachable: true},
		{ID: 4, GroupID: "", InterfaceID: 1, Name: "old", Address: "old.example.com", Priority: 1, Enabled: true, Reachable: true},
	}
	gs := groupHosts(rows)
	if len(gs) != 2 {
		t.Fatalf("want 2 groups, got %d", len(gs))
	}
	if gs[0].GroupID != "fallback_4" || gs[0].Remark != "old" {
		t.Errorf("the pre-group row should come first as its own group: %+v", gs[0])
	}
	g := gs[1]
	if len(g.InterfaceIDs) != 2 || len(g.Hosts) != 2 || g.Hosts[1] != "b.example.com:443" || g.Reachable || g.LastError != "timeout" {
		t.Errorf("group = %+v", g)
	}
	back := rowsFor("g", g)
	if len(back) != 4 {
		t.Errorf("rows for 2 hosts x 2 inbounds should be 4, got %d", len(back))
	}
}
