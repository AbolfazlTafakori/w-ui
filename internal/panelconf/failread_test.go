package panelconf

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
)

// failFirstRead makes the next read fail, once -- in Save, the read of the
// current settings it starts with.
func failFirstRead(t *testing.T, db *gorm.DB) *atomic.Bool {
	t.Helper()
	armed := &atomic.Bool{}
	err := db.Callback().Query().Before("gorm:query").Register("test:fail_first_settings_read", func(tx *gorm.DB) {
		if armed.CompareAndSwap(true, false) {
			_ = tx.AddError(errors.New("the disk said no"))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return armed
}

// Saving the settings when the current ones cannot be read is refused, and
// the bot token is left as it was. Before, the failed read was ignored and
// a token left as its placeholder was saved as no token at all: the bot
// stopped, with nothing said.
func TestASaveThatCannotReadTheTokensKeepsThem(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := database.PutSetting(db, keyNotifyToken, "123:REAL-BOT-TOKEN"); err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(db, "en")
	got, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The page sends the token back as its placeholder when it was not
	// retyped.
	got.NotifyBotToken = TokenPlaceholder
	// A panel just started has read nothing yet: its first read is the one
	// Save makes, and that is the one that fails.
	fresh := NewSettings(db, "en")
	armed := failFirstRead(t, db)
	armed.Store(true)
	if _, err := fresh.Save(ctx, got); err == nil {
		t.Error("a save that could not read the current settings went through")
	}
	v, ok, err := database.GetSetting(db, keyNotifyToken)
	if err != nil || !ok || v != "123:REAL-BOT-TOKEN" {
		t.Fatalf("the bot token is now %q (%v %v), want it as it was", v, ok, err)
	}
}
