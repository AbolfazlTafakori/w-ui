package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abolfazl/w-ui/internal/database"
)

// A reseller cannot read the subscription settings, which are the owner's,
// so the customer's link says itself whether the service is on. Without it
// the QR dialog had no way to know, and showed a reseller no link (#13).
func TestACustomersLinkSaysWhetherTheServiceIsOn(t *testing.T) {
	s, _ := revealServer(t)
	ask := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/clients/1/subscription", nil)
		req.SetPathValue("id", "1")
		rec := httptest.NewRecorder()
		s.handleClientSubscription(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := ask(); got["enabled"] != false || got["link"] == "" {
		t.Fatalf("service off: %v", got)
	}
	if err := database.PutSetting(dbOf(s), "sub.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if got := ask(); got["enabled"] != true || got["link"] == "" {
		t.Fatalf("service on: %v", got)
	}
}
