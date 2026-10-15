package nodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func TestPGNames(t *testing.T) {
	for name, want := range map[string]bool{"wui_12": true, "wui_": false, "wui_test1": false, "admin": false, "wui-3": false} {
		if pgOwns(name) != want {
			t.Errorf("pgOwns(%q) != %v", name, want)
		}
	}
	if !pgOwns(pgUsername(&model.Account{ID: 9})) {
		t.Fatal("a name this panel makes is not its own")
	}
	if pgRemoteID("VLESS TCP") != pgRemoteID("VLESS TCP") || pgRemoteID("a") == pgRemoteID("b") || pgRemoteID("x") < 0 {
		t.Fatal("inbound numbers are not stable and distinct")
	}
	long := strings.Repeat("x", 80)
	if g := pgGroupName(long); len(g) > 64 || pgGroupName(long) != g {
		t.Fatalf("group name %q", g)
	}
	if pgGroupName("VLESS TCP") != "wui VLESS TCP" {
		t.Fatal("short group name")
	}
}

func TestPGPortOf(t *testing.T) {
	var a, b pgInbound
	_ = json.Unmarshal([]byte(`{"tag":"a","port":443}`), &a)
	_ = json.Unmarshal([]byte(`{"tag":"b","port":"8443"}`), &b)
	if a.portOf() != 443 || b.portOf() != 8443 {
		t.Fatalf("%d %d", a.portOf(), b.portOf())
	}
}

// The API key goes in X-Api-Key, users are read a page at a time, and only
// the links of a subscription are kept.
func TestPGAPI(t *testing.T) {
	var sawKey string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/users":
			sawKey = r.Header.Get("X-Api-Key")
			if r.URL.Query().Get("search") != "wui_" {
				t.Errorf("search %q", r.URL.Query().Get("search"))
			}
			if r.URL.Query().Get("offset") == "0" {
				users := make([]map[string]any, 500)
				for i := range users {
					users[i] = map[string]any{"username": "wui_1", "used_traffic": 1}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"users": users, "total": 501})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []any{map[string]any{"username": "wui_2"}}, "total": 501})
		case strings.HasSuffix(r.URL.Path, "/links"):
			if r.Header.Get("X-Api-Key") != "" {
				t.Error("the API key went to a subscription link")
			}
			_, _ = w.Write([]byte("vless://a@h:1#x\n\nnot a link\ntrojan://b@h:2#y\n"))
		}
	}))
	defer srv.Close()
	api, err := newPGAPI(model.Node{Address: srv.URL, Token: "k3y", TLSMode: model.TLSSkip, AllowPrivateAddress: true})
	if err != nil {
		t.Fatal(err)
	}
	us, err := api.ourUsers(context.Background())
	if err != nil || len(us) != 501 || sawKey != "k3y" {
		t.Fatalf("%d users, key %q, %v", len(us), sawKey, err)
	}
	links, err := api.links(context.Background(), srv.URL+"/sub/tok")
	if err != nil || len(links) != 2 {
		t.Fatalf("%v %v", links, err)
	}
}
