package tgbot

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// A customer asking for their files when the panel cannot read their plans
// is told to try again -- not "nothing found", which reads as their plan
// being gone. Before, the failed read was ignored and an empty answer was
// taken for no plans.
func TestACustomerIsNotToldTheirPlanIsGoneWhenItCouldNotBeRead(t *testing.T) {
	f := newFixture(t)
	armed := &atomic.Bool{}
	if err := f.db.Callback().Query().Before("gorm:query").Register("test:fail_next_read", func(tx *gorm.DB) {
		if armed.CompareAndSwap(true, false) {
			_ = tx.AddError(errors.New("the disk said no"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	f.press(customerID, "my:sub")
	said := f.tg.said()
	if strings.Contains(said, f.bot.t("noResult")) || !strings.Contains(said, f.bot.t("tryAgain")) {
		t.Fatalf("told %q, want to try again", said)
	}
}
