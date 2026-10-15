package nodes

import (
	"encoding/json"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestSince(t *testing.T) {
	if since(150, 100) != 50 {
		t.Fatal("growth")
	}
	if since(30, 100) != 30 {
		t.Fatal("a counter started again counts all of what it has")
	}
	if since(100, 100) != 0 {
		t.Fatal("unchanged")
	}
}

func TestObjectOrString(t *testing.T) {
	var a, b xuiSettings
	if err := objectOrString(json.RawMessage(`{"clients":[{"email":"wui-1"}],"method":"m"}`), &a); err != nil || len(a.Clients) != 1 || a.Method != "m" {
		t.Fatalf("object: %+v %v", a, err)
	}
	if err := objectOrString(json.RawMessage(`"{\"clients\":[{\"email\":\"wui-2\"}]}"`), &b); err != nil || len(b.Clients) != 1 || b.Clients[0].Email != "wui-2" {
		t.Fatalf("string: %+v %v", b, err)
	}
	var c xuiSettings
	if err := objectOrString(json.RawMessage(`""`), &c); err != nil {
		t.Fatal(err)
	}
}

func TestXUIClientFor(t *testing.T) {
	acc := &model.Account{ID: 7, Secret: "s3cret"}
	c := model.Client{Name: "ali", DeviceLimit: 1}
	got := xuiClientFor(acc, model.XUIInbound{Kind: "vless", Flow: "xtls-rprx-vision"}, c, true)
	if got.Email != "wui-7" || got.ID != "s3cret" || got.Flow != "xtls-rprx-vision" || got.LimitIP != 1 || !got.Enable ||
		got.TotalGB != 0 || got.ExpiryTime != 0 {
		t.Fatalf("vless: %+v", got)
	}
	if got := xuiClientFor(acc, model.XUIInbound{Kind: "trojan"}, c, false); got.Password != "s3cret" || got.ID != "" || got.Enable {
		t.Fatalf("trojan: %+v", got)
	}
	if got := xuiClientFor(acc, model.XUIInbound{Kind: "hysteria"}, c, true); got.Auth != "s3cret" {
		t.Fatalf("hysteria: %+v", got)
	}
	c.DeviceLimit = 0
	if got := xuiClientFor(acc, model.XUIInbound{Kind: "vmess"}, c, true); got.LimitIP != 0 {
		t.Fatalf("a customer with no limit is given one there: %+v", got)
	}
}

func TestXUIDiffers(t *testing.T) {
	want := xuiClient{Email: "wui-1", ID: "u", Enable: true, LimitIP: 1}
	if xuiDiffers(xuiInboundClient{Email: "wui-1", ID: "u", Enable: true, LimitIP: 1}, want) {
		t.Fatal("the same client reads as changed")
	}
	if !xuiDiffers(xuiInboundClient{Email: "wui-1", ID: "u", Enable: false, LimitIP: 1}, want) {
		t.Fatal("a disabled client reads as unchanged")
	}
}
