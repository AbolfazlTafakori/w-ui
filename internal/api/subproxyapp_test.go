package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// A proxy app asking for a customer none of whose files it can import is
// told so, not handed an empty list it would take as the subscription
// emptied; with a file it can import, it gets that file's link.
func TestAProxyAppIsNotHandedAnEmptyList(t *testing.T) {
	registerWG.Do(wgdriver.Register)
	s, _ := revealServer(t) // a customer with one L2TP file
	s.subMiss = newSubMisses()
	for k, v := range map[string]string{"sub.enabled": "true", "sub.path": "/sub/"} {
		if err := database.PutSetting(dbOf(s), k, v); err != nil {
			t.Fatal(err)
		}
	}
	router := s.SubscriptionRouter(http.NotFoundHandler())
	fetch := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+revealToken, nil)
		req.Header.Set("User-Agent", "v2rayNG/1.9.16")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := fetch(); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "browser") {
		t.Fatalf("nothing for the app: %d %q", rec.Code, rec.Body.String())
	}

	wg := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Enabled: true,
		Subnet: "10.66.0.0/24", EndpointHost: "vpn.example.com", NodeID: 1, MTU: 1420,
		PublicKey: "Q2VydGFpbmx5IG5vdCBhIHJlYWwga2V5IGF0IGFsbCE=", PrivateKey: "c2VydmVyIHByaXZhdGUga2V5IGZvciB0aGUgdGVzdHM="}
	if err := dbOf(s).Create(&wg).Error; err != nil {
		t.Fatal(err)
	}
	var c model.Client
	dbOf(s).Where("sub_token = ?", revealToken).First(&c)
	if err := dbOf(s).Create(&model.Account{ClientID: c.ID, InterfaceID: wg.ID, NodeID: 1, DeviceName: "device-1", IP: "10.66.0.2", Enabled: true,
		PrivateKey: "YWNjb3VudCBwcml2YXRlIGtleSBmb3IgdGhlIHRlc3Q=", PublicKey: "YWNjb3VudCBwdWJsaWMga2V5IGZvciB0aGUgdGVzdHM="}).Error; err != nil {
		t.Fatal(err)
	}
	rec := fetch()
	body, _ := base64.StdEncoding.DecodeString(rec.Body.String())
	if rec.Code != http.StatusOK || !strings.HasPrefix(string(body), "wireguard://") {
		t.Fatalf("a WireGuard file for the app: %d %q", rec.Code, body)
	}
}
