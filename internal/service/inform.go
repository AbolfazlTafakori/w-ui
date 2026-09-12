package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Informer posts each customer's traffic to an outside URL, the way 3x-ui's
// external traffic inform does, so a billing system can be told without
// polling the panel. Only what changed since the last post is sent.
type Informer struct {
	db       *gorm.DB
	settings *Settings
	log      *slog.Logger
	client   *http.Client

	last map[uint][2]uint64 // client id -> up, down at the last post
}

func NewInformer(db *gorm.DB, settings *Settings, log *slog.Logger) *Informer {
	return &Informer{db: db, settings: settings, log: log,
		client: &http.Client{Timeout: 20 * time.Second}, last: map[uint][2]uint64{}}
}

type informRow struct {
	Email string `json:"email"`
	Up    uint64 `json:"up"`
	Down  uint64 `json:"down"`
}

// Run posts every interval until ctx ends.
func (i *Informer) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				i.tick(ctx)
			}
		}
	}()
}

func (i *Informer) tick(ctx context.Context) {
	cfg, err := i.settings.Get(ctx)
	if err != nil || !cfg.ExternalTrafficInformEnable || cfg.ExternalTrafficInformURI == "" {
		return
	}
	var clients []model.Client
	if err := i.db.WithContext(ctx).Select("id, name, up_bytes, down_bytes").Find(&clients).Error; err != nil {
		return
	}
	var rows []informRow
	next := make(map[uint][2]uint64, len(clients))
	for _, c := range clients {
		next[c.ID] = [2]uint64{c.UpBytes, c.DownBytes}
		prev, seen := i.last[c.ID]
		up, down := c.UpBytes, c.DownBytes
		if seen {
			if c.UpBytes >= prev[0] {
				up = c.UpBytes - prev[0]
			}
			if c.DownBytes >= prev[1] {
				down = c.DownBytes - prev[1]
			}
		}
		if up == 0 && down == 0 {
			continue
		}
		rows = append(rows, informRow{Email: c.Name, Up: up, Down: down})
	}
	i.last = next
	if len(rows) == 0 {
		return
	}
	body, _ := json.Marshal(rows)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ExternalTrafficInformURI, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := i.client.Do(req)
	if err != nil {
		i.log.Warn("could not inform the external traffic endpoint", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		i.log.Warn("the external traffic endpoint refused the report", "status", resp.Status)
	}
}
