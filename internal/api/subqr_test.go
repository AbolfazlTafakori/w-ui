package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// A file's QR is an image the page asks for when it is opened, not drawn
// into every page; it is the same QR the page drew, and a file whose keys
// change is a new address, so a kept image is never of old keys.
var registerWG sync.Once

func TestAQRIsAskedForWhenOpened(t *testing.T) {
	registerWG.Do(wgdriver.Register)
	s, _ := revealServer(t)
	s.subMiss = newSubMisses()
	for k, v := range map[string]string{"sub.enabled": "true", "sub.path": "/sub/"} {
		if err := database.PutSetting(s.db, k, v); err != nil {
			t.Fatal(err)
		}
	}
	// A WireGuard file beside the L2TP one, which has no QR.
	wg := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Enabled: true,
		Subnet: "10.66.0.0/24", EndpointHost: "vpn.example.com", NodeID: 1, MTU: 1420,
		PublicKey: "Q2VydGFpbmx5IG5vdCBhIHJlYWwga2V5IGF0IGFsbCE=", PrivateKey: "c2VydmVyIHByaXZhdGUga2V5IGZvciB0aGUgdGVzdHM="}
	if err := s.db.Create(&wg).Error; err != nil {
		t.Fatal(err)
	}
	var c model.Client
	s.db.Where("sub_token = ?", revealToken).First(&c)
	acc := model.Account{ClientID: c.ID, InterfaceID: wg.ID, NodeID: 1, DeviceName: "device-1", IP: "10.66.0.2", Enabled: true,
		PrivateKey: "YWNjb3VudCBwcml2YXRlIGtleSBmb3IgdGhlIHRlc3Q=", PublicKey: "YWNjb3VudCBwdWJsaWMga2V5IGZvciB0aGUgdGVzdHM="}
	if err := s.db.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}
	router := s.SubscriptionRouter(http.NotFoundHandler())
	open := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	page := open("/sub/" + revealToken)
	html := page.Body.String()
	if strings.Contains(html, "data:image/png") {
		t.Fatalf("QR codes are still drawn into the page: %d", strings.Count(html, "data:image/png"))
	}
	// Asked for only when opened: a hidden image's src is fetched anyway.
	if strings.Contains(html, ` src="?view=qr`) {
		t.Fatal("a QR is fetched with the page, not when opened")
	}
	srcs := regexp.MustCompile(`data-src="(\?view=qr[^"]*)"`).FindAllStringSubmatch(html, -1)
	if len(srcs) != 2 { // the link's, and the WireGuard file's; none for L2TP
		t.Fatalf("%d QR addresses on the page, want 2: %v", len(srcs), srcs)
	}
	for _, m := range srcs {
		src := strings.ReplaceAll(m[1], "&amp;", "&")
		rec := open("/sub/" + revealToken + src)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
			t.Fatalf("%s: %d %s", src, rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "private") {
			t.Fatalf("%s may be kept by others", src)
		}
	}
	if rec := open("/sub/" + revealToken + "?view=qr&ref=999.0"); rec.Code != http.StatusNotFound {
		t.Fatalf("a file that is not there: %d", rec.Code)
	}

	// New keys, a new address.
	before := srcs[1][1]
	if err := s.db.Model(&acc).Update("private_key", "bmV3IGFjY291bnQgcHJpdmF0ZSBrZXkgZm9yIHRlc3Q=").Error; err != nil {
		t.Fatal(err)
	}
	again := regexp.MustCompile(`data-src="(\?view=qr[^"]*)"`).FindAllStringSubmatch(open("/sub/"+revealToken).Body.String(), -1)
	if len(again) != 2 || again[1][1] == before {
		t.Fatalf("the file's keys changed and its QR's address did not: %s", before)
	}
}

// A preview has no link to ask with: its QR codes are drawn in.
func TestAPreviewDrawsItsQRs(t *testing.T) {
	v := newSubView(&service.SubPage{Name: "A", Status: "active", SubURL: "https://example.com/sub/x",
		Devices: []service.SubPageDevice{{ID: 1, Protocol: "wireguard", Config: "[Interface]\nPrivateKey = x\n"}}}, "", true, "")
	if !strings.HasPrefix(string(v.SubQR), "data:image/png") || !strings.HasPrefix(string(v.Devices[0].QR), "data:image/png") {
		t.Fatalf("a preview's QR codes: %q %q", v.SubQR[:20], v.Devices[0].QR)
	}
}
