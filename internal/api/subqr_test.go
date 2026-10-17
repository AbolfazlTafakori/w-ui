package api

import (
	"encoding/base64"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

var registerWG sync.Once

var qrAttr = regexp.MustCompile(`data-qr="([^"]*)"`)

// dotsOf reads a pattern back into its rows of dots.
func dotsOf(t *testing.T, code string) [][]bool {
	t.Helper()
	at := strings.IndexByte(code, ':')
	n, err := strconv.Atoi(code[:at])
	if err != nil || n < 21 {
		t.Fatalf("a QR %q wide", code[:at])
	}
	bits, err := base64.RawURLEncoding.DecodeString(code[at+1:])
	if err != nil || len(bits) != (n*n+7)/8 {
		t.Fatalf("a QR of %d dots in %d bytes: %v", n*n, len(bits), err)
	}
	out := make([][]bool, n)
	for y := range out {
		out[y] = make([]bool, n)
		for x := range out[y] {
			i := y*n + x
			out[y][x] = bits[i/8]&(0x80>>(i%8)) != 0
		}
	}
	return out
}

// isQROf reports whether code is text's QR, dot for dot.
func isQROf(t *testing.T, code, text string) bool {
	t.Helper()
	q, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		t.Fatal(err)
	}
	q.DisableBorder = true
	want, got := q.Bitmap(), dotsOf(t, code)
	if len(want) != len(got) {
		return false
	}
	for y := range want {
		for x := range want[y] {
			if want[y][x] != got[y][x] {
				return false
			}
		}
	}
	return true
}

// A QR is in the page as its pattern, drawn when it is opened: no image in
// every page, and no request between the tap and the code. It is the QR of
// what the file holds, and a file whose keys change has a new one.
func TestAQRIsInThePageAsItsPattern(t *testing.T) {
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
	open := func() string {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+revealToken, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	page := open()
	if strings.Contains(page, "data:image/png") || strings.Contains(page, "view=qr") {
		t.Fatal("a QR is an image in the page, or one asked for when opened")
	}
	codes := qrAttr.FindAllStringSubmatch(page, -1)
	if len(codes) != 2 { // the link's, and the WireGuard file's; none for L2TP
		t.Fatalf("%d QR codes on the page, want 2", len(codes))
	}
	link := regexp.MustCompile(`data-text="(http[^"]*)"`).FindStringSubmatch(page)
	if link == nil || !isQROf(t, codes[0][1], html.UnescapeString(link[1])) {
		t.Fatal("the link's QR is not of the link")
	}
	file := regexp.MustCompile(`(?s)<code class="cfg-text">(\[Interface\].*?)</code>`).FindStringSubmatch(page)
	if file == nil || !isQROf(t, codes[1][1], html.UnescapeString(file[1])) {
		t.Fatal("the file's QR is not of the file")
	}

	// New keys, a new QR.
	if err := s.db.Model(&acc).Update("private_key", "bmV3IGFjY291bnQgcHJpdmF0ZSBrZXkgZm9yIHRlc3Q=").Error; err != nil {
		t.Fatal(err)
	}
	again := qrAttr.FindAllStringSubmatch(open(), -1)
	if len(again) != 2 || again[1][1] == codes[1][1] {
		t.Fatal("the file's keys changed and its QR did not")
	}
}

// A preview's QR codes are drawn by the page as a customer's are.
func TestAPreviewCarriesItsQRs(t *testing.T) {
	cfg := "[Interface]\nPrivateKey = x\n"
	v := newSubView(&service.SubPage{Name: "A", Status: "active", SubURL: "https://example.com/sub/x",
		Devices: []service.SubPageDevice{{ID: 1, Protocol: "wireguard", Config: cfg}}}, "", true, "")
	if !isQROf(t, v.SubQR, "https://example.com/sub/x") || !isQROf(t, v.Devices[0].QR, cfg) {
		t.Fatal("a preview's QR codes")
	}
}

// A pattern is worked out once and kept, and the keeping is bounded.
func TestAQRIsWorkedOutOnce(t *testing.T) {
	if subQR("") != "" {
		t.Fatal("nothing to scan has a QR")
	}
	a := subQR("wireguard://kept-once")
	if b := subQR("wireguard://kept-once"); a != b {
		t.Fatal("the same text, two QR codes")
	}
	for i := 0; i < subQRKept+10; i++ {
		subQR("vless://" + strconv.Itoa(i))
	}
	subQRs.Lock()
	n := len(subQRs.kept)
	subQRs.Unlock()
	if n > subQRKept {
		t.Fatalf("%d kept, more than %d", n, subQRKept)
	}
}
