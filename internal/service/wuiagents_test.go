package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestTheWuiProfileCarriesAgentDevicesWithTheirSignIn(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	mk := func(i model.Interface) uint {
		i.Enabled, i.Subnet = true, "10.80.0.0/24"
		if err := db.Create(&i).Error; err != nil {
			t.Fatal(err)
		}
		return i.ID
	}
	ike := mk(model.Interface{Name: "ike0", Protocol: model.ProtocolIKEv2, EndpointHost: "ike.example.com",
		IKEv2: model.JSONField[model.IKEv2Params]{V: model.IKEv2Params{MSCHAPv2: true, PSK: true, CACert: "-----BEGIN CERTIFICATE-----\nX\n-----END CERTIFICATE-----\n"}}})
	oc := mk(model.Interface{Name: "oc0", Protocol: model.ProtocolOpenConnect, EndpointHost: "oc.example.com", ListenPort: 4443})
	l2 := mk(model.Interface{Name: "l2tp0", Protocol: model.ProtocolL2TP, EndpointHost: "l2.example.com",
		L2TP: model.JSONField[model.L2TPParams]{V: model.L2TPParams{PSK: "tunnel-key"}}})
	pp := mk(model.Interface{Name: "pptp0", Protocol: model.ProtocolPPTP, EndpointHost: "pp.example.com"})
	db.Create(&model.Host{InterfaceID: l2, Name: "spare", Enabled: true, Address: "spare.example.com", Priority: 1})
	db.Create(&model.Host{InterfaceID: l2, Name: "hidden", Enabled: true, Address: "hidden.example.com", Priority: 2, ExcludeFormats: "zip,wui"})

	c := model.Client{Name: "Sara", Protocol: model.ProtocolIKEv2, Status: model.StatusActive, DeviceLimit: 4}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	for i, id := range []uint{ike, oc, l2, pp} {
		acc := model.Account{ClientID: c.ID, InterfaceID: id, DeviceName: "phone", IP: "10.80.0." + string(rune('2'+i)),
			Username: "user-" + string(rune('a'+i)), Secret: "pw-" + string(rune('a'+i))}
		if err := db.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}

	s := newSubs(db, nil)
	token, _ := s.EnsureToken(ctx, c.ID)
	b, err := s.Serve(ctx, token, "wui")
	if err != nil {
		t.Fatalf("a customer with only agent devices gets a profile: %v", err)
	}
	var prof WUIProfile
	if err := json.Unmarshal(b.Body, &prof); err != nil {
		t.Fatal(err)
	}
	if prof.Tunnels == nil || len(prof.Tunnels) != 0 || !strings.Contains(string(b.Body), `"tunnels": []`) {
		t.Fatalf("tunnels must stay an empty list for older apps: %s", b.Body)
	}
	if len(prof.Agents) != 4 {
		t.Fatalf("agents: %+v", prof.Agents)
	}
	by := map[string]WUIAgent{}
	for _, a := range prof.Agents {
		by[a.Protocol] = a
	}

	k := by["ikev2"].IKEv2
	if k == nil || k.Servers[0] != "ike.example.com" || k.Port != 500 || k.ServerID != "ike.example.com" ||
		strings.Join(k.Methods, ",") != "mschapv2,psk" || k.Username != "user-a" || k.Password != "pw-a" ||
		!strings.HasPrefix(k.CACert, "-----BEGIN CERTIFICATE-----") || k.P12 != "" {
		t.Fatalf("ikev2: %+v", k)
	}
	o := by["openconnect"].OpenConnect
	if o == nil || o.URL != "https://oc.example.com:4443" || o.Username != "user-b" || o.Password != "pw-b" {
		t.Fatalf("openconnect: %+v", o)
	}
	l := by["l2tp"].L2TP
	if l == nil || strings.Join(l.Servers, " ") != "l2.example.com spare.example.com" || l.Port != 1701 ||
		l.PSK != "tunnel-key" || l.Username != "user-c" || l.Password != "pw-c" {
		t.Fatalf("l2tp (a host excluded from wui is left out): %+v", l)
	}
	p := by["pptp"].PPTP
	if p == nil || p.Servers[0] != "pp.example.com" || p.Port != 1723 || p.Username != "user-d" || p.Password != "pw-d" {
		t.Fatalf("pptp: %+v", p)
	}
	if a := by["pptp"]; a.Name != "pptp0" || a.Device != "phone" || a.L2TP != nil || a.IKEv2 != nil {
		t.Fatalf("one block per entry: %+v", a)
	}

	// The links and the zip keep withholding the sign-in.
	z, err := s.Serve(ctx, token, "links")
	if err == nil && strings.Contains(string(z.Body), "pw-c") {
		t.Fatal("links carry an agent password")
	}
}

func TestAWireGuardOnlyProfileHasNoAgentsKey(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	db.Model(&model.Account{}).Where("client_id = ?", c.ID).Update("private_key", "priv")
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(ctx, c.ID)
	b, err := s.Serve(ctx, token, "wui")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b.Body), `"agents"`) {
		t.Fatalf("agents key on a WireGuard-only profile: %s", b.Body)
	}
}
