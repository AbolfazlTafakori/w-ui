package reconciler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An end sent in another zone ends on time. SQLite keeps times as text and
// compares them as text, so a row kept as "16:00+03:30" would sort after a
// query's "13:00+00:00" and a customer would stay on for hours past their
// end. The driver writes every time as UTC, which is what makes the sweep
// right; this holds it to that.
func TestAnEndSentInAnotherZoneEndsOnTime(t *testing.T) {
	r, db, _, _ := newRig(t)
	tehran := time.FixedZone("IRST", 3*3600+1800)
	past := time.Now().Add(-30 * time.Minute).In(tehran)
	id := seed(t, db, model.Client{Name: "Zone", ExpiresAt: &past}, "10.66.0.9")

	var raw string
	db.Raw("SELECT CAST(expires_at AS TEXT) FROM clients WHERE id = ?", id).Scan(&raw)
	if !strings.HasSuffix(raw, "+00:00") && !strings.HasSuffix(raw, "Z") {
		t.Fatalf("the end was kept as %q, not in UTC: the expiry sweep compares it as text", raw)
	}

	r.Tick(context.Background())
	var got model.Client
	db.First(&got, id)
	if got.Status != model.StatusExpired {
		t.Fatalf("a customer whose end passed half an hour ago, sent in +03:30, is %q", got.Status)
	}
}
