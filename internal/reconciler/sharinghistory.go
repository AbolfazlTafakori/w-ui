package reconciler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// Keeping the record.
//
// The sharing report is what is true now: a device stops being on it ten
// minutes after its second place goes quiet, and a held-off device is only a
// line in the log. An operator deciding whether a customer keeps doing it
// needs what was true last week too, and after the customer has been
// deleted. So every time a device shows on the report, and every time one is
// held off, it is written down here, and stays.
//
// One sighting that goes on is one record: a device on the report minute
// after minute extends the record it has, rather than adding one a minute.

const (
	// historyEvery is how often the live report is written down. The report
	// looks ten minutes back, so a minute misses nothing.
	historyEvery = time.Minute

	// historyGap is how long a record can go unseen and still be the same
	// one when the device shows again. Past it, it is a new time.
	historyGap = 30 * time.Minute

	// historyRetention is how long a record is kept: long enough to be the
	// customer's history, short enough that the table stays small.
	historyRetention = 400 * 24 * time.Hour
)

// SharingHistory is the record, newest first.
func (r *Reconciler) SharingHistory(ctx context.Context, limit int) ([]model.SharingEvent, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	var out []model.SharingEvent
	err := r.db.WithContext(ctx).Order("last_seen DESC, id DESC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("sharing history: %w", err)
	}
	ids := make([]uint, 0, len(out))
	for _, ev := range out {
		if ev.ClientID != 0 {
			ids = append(ids, ev.ClientID)
		}
	}
	var live []uint
	if len(ids) > 0 {
		if err := r.db.WithContext(ctx).Model(&model.Client{}).Where("id IN ?", ids).Pluck("id", &live).Error; err != nil {
			return nil, fmt.Errorf("sharing history: %w", err)
		}
	}
	exists := make(map[uint]bool, len(live))
	for _, id := range live {
		exists[id] = true
	}
	for i := range out {
		out[i].Deleted = !exists[out[i].ClientID]
	}
	return out, nil
}

// maybeRecordSharing writes the live report down, once a minute.
func (r *Reconciler) maybeRecordSharing(ctx context.Context, now time.Time) {
	r.mu.Lock()
	due := now.Sub(r.lastHistory) >= historyEvery
	if due {
		r.lastHistory = now
	}
	r.mu.Unlock()
	if !due {
		return
	}
	reports, err := r.Sharing(ctx)
	if err != nil {
		r.log.Warn("could not read the sharing report for its record", "error", err)
		return
	}
	for _, rep := range reports {
		ev := model.SharingEvent{
			Kind:       model.SharingShared,
			AccountID:  rep.AccountID,
			ClientID:   rep.ClientID,
			ClientName: rep.ClientName,
			DeviceName: rep.DeviceName,
			Addrs:      strings.Join(rep.Addrs, ","),
			Source:     model.SharingLive,
			StartedAt:  rep.Since.UTC(),
			LastSeen:   now,
		}
		created, err := recordSharing(ctx, r.db, ev)
		if err != nil {
			r.log.Warn("could not record a shared device", "device", rep.DeviceName, "error", err)
			continue
		}
		if created {
			// Said once, when it begins: the record grows quietly after.
			r.log.Info("device used from several places at once",
				"client", rep.ClientName, "device", rep.DeviceName, "from", strings.Join(rep.Addrs, ","))
			r.notifySharing(rep)
		}
	}
	if err := r.db.WithContext(ctx).Where("last_seen < ?", now.Add(-historyRetention)).
		Delete(&model.SharingEvent{}).Error; err != nil {
		r.log.Warn("could not prune the sharing record", "error", err)
	}
}

// recordHeld writes down a device held off for going over its plan.
func (r *Reconciler) recordHeld(ctx context.Context, c *model.Client, h heldAccount, where string, now time.Time) {
	var addrs []string
	r.conc.mu.Lock()
	if a := r.conc.acts[h.Account.ID]; a != nil {
		addrs = a.liveAddrs(now)
	}
	r.conc.mu.Unlock()
	ev := model.SharingEvent{
		Kind:       model.SharingHeld,
		AccountID:  h.Account.ID,
		ClientID:   c.ID,
		ClientName: c.Name,
		DeviceName: h.Account.DeviceName,
		Addrs:      strings.Join(addrs, ","),
		Limit:      c.DeviceLimit,
		Place:      where,
		Source:     model.SharingLive,
		StartedAt:  now,
		LastSeen:   now,
	}
	if _, err := recordSharing(ctx, r.db, ev); err != nil {
		r.log.Warn("could not record a held-off device", "device", h.Account.DeviceName, "error", err)
	}
}

// notifySharing sends the sharing notice, for those who turned it on.
func (r *Reconciler) notifySharing(rep SharingReport) {
	if r.notifier == nil {
		return
	}
	r.notifier.Send(notify.Event{
		Kind:  notify.KindSharing,
		Title: "A file is being used from several places",
		Body: fmt.Sprintf("%s / %s — %d addresses at once: %s", rep.ClientName, rep.DeviceName,
			len(rep.Addrs), strings.Join(rep.Addrs, ", ")),
		At: time.Now().UTC(),
	})
}

// recordSharing adds ev to the record, or extends the record of the same
// device and kind that was last seen within historyGap: its last sighting
// moves on, and its addresses gather every one seen. created says which.
func recordSharing(ctx context.Context, db *gorm.DB, ev model.SharingEvent) (created bool, err error) {
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var open model.SharingEvent
		q := tx.Where("kind = ? AND client_name = ? AND device_name = ? AND last_seen >= ? AND started_at <= ?",
			ev.Kind, ev.ClientName, ev.DeviceName, ev.StartedAt.Add(-historyGap), ev.LastSeen).
			Order("last_seen DESC")
		if ev.AccountID != 0 {
			q = q.Where("account_id IN ?", []uint{0, ev.AccountID})
		}
		err := q.First(&open).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			created = true
			return tx.Create(&ev).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]any{"addrs": mergeAddrs(open.Addrs, ev.Addrs)}
		if ev.LastSeen.After(open.LastSeen) {
			updates["last_seen"] = ev.LastSeen
		}
		if ev.StartedAt.Before(open.StartedAt) {
			updates["started_at"] = ev.StartedAt
		}
		if open.AccountID == 0 && ev.AccountID != 0 {
			updates["account_id"], updates["client_id"] = ev.AccountID, ev.ClientID
		}
		if ev.Limit != 0 {
			updates["plan_limit"] = ev.Limit
		}
		if ev.Place != "" {
			updates["place"] = ev.Place
		}
		return tx.Model(&model.SharingEvent{}).Where("id = ?", open.ID).Updates(updates).Error
	})
	return created && err == nil, err
}

// mergeAddrs is the addresses of both lists, once each, in order.
func mergeAddrs(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{a, b} {
		for _, x := range strings.Split(list, ",") {
			if x = strings.TrimSpace(x); x != "" && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
