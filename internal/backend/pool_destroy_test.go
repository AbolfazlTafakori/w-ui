package backend

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A pass that read the interfaces before one was deleted does not bring it
// back; one created afterwards under the same id is opened as usual.
func TestADeletedTunnelIsNotOpenedAgain(t *testing.T) {
	p := NewPool(slog.New(slog.NewTextHandler(io.Discard, nil)))
	iface := model.Interface{ID: 7, Name: "gone", Protocol: "nothing", Enabled: true,
		CreatedAt: time.Now().Add(-time.Hour)}

	p.Destroy(context.Background(), &iface)
	p.Sync(context.Background(), []model.Interface{iface}) // the stale list
	if _, tried := p.open[7]; tried {
		t.Fatal("a stale pass reopened a deleted tunnel")
	}

	again := iface
	again.CreatedAt = time.Now().Add(time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	p.Sync(context.Background(), []model.Interface{again})
	if _, tried := p.open[7]; !tried {
		t.Fatal("a tunnel created after the deletion was never opened")
	}
}
