package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/logger"
)

// The time before the record was kept.
//
// Releases before this one kept no record, but they logged what they saw:
// every device held off for going over its plan, and every device that
// connected and where from. Read back once, those are the record of that
// time. A held-off device is in the log as it happened. A device used from
// several places is not -- the report was never logged -- so it is found the
// way the report finds it: one device connecting from sharingThreshold
// addresses inside sharingWindow. A device whose places took turns without it
// ever going quiet logged one connection, so this finds fewer than the report
// would have; what it finds, it saw.

// KeySharingImport is where the result of reading the log back is kept.
const KeySharingImport = "sharing.historyImport"

// Log lines this reads, as the reconciler writes them.
const (
	logHeldOff   = "connection limit reached; device held off"
	logConnected = "device connected"
)

// SharingImport is what reading the log back came to.
type SharingImport struct {
	// State is "done", "denied" (the panel's user cannot read the journal),
	// "unavailable" (no journal on this system) or "failed".
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Held   int       `json:"held"`
	Shared int       `json:"shared"`
	Error  string    `json:"error,omitempty"`
}

// SharingImportStatus is the last result, if the log has been read.
func (r *Reconciler) SharingImportStatus() (SharingImport, bool) {
	raw, ok, err := database.GetSetting(r.db, KeySharingImport)
	if err != nil || !ok {
		return SharingImport{}, false
	}
	var s SharingImport
	if json.Unmarshal([]byte(raw), &s) != nil {
		return SharingImport{}, false
	}
	return s, true
}

// ImportSharingOnce reads the log back the first time the panel starts with
// a record to keep, and never again on its own: what came after is recorded
// as it happens.
func (r *Reconciler) ImportSharingOnce(ctx context.Context) {
	if _, done := r.SharingImportStatus(); done {
		return
	}
	res := r.ImportSharingFromLog(ctx)
	r.log.Info("sharing record read back from the log",
		"state", res.State, "held", res.Held, "shared", res.Shared, "error", res.Error)
}

// ImportSharingFromLog reads the log back into the record. Running it again
// adds nothing twice: what it finds extends the records it found before.
func (r *Reconciler) ImportSharingFromLog(ctx context.Context) SharingImport {
	now := time.Now().UTC()
	res := SharingImport{State: "done", At: now}

	accounts, err := r.accountsByName(ctx)
	if err != nil {
		res.State, res.Error = "failed", err.Error()
		r.saveImport(res)
		return res
	}

	connects := map[string][]sighting{}
	err = logger.ScanJournal(ctx, now.Add(-historyRetention), []string{logHeldOff, logConnected}, func(e logger.Entry) {
		switch e.Message {
		case logHeldOff:
			ev, ok := heldFromLog(e, accounts)
			if !ok {
				return
			}
			if created, err := recordSharing(ctx, r.db, ev); err == nil && created {
				res.Held++
			}
		case logConnected:
			name, _ := e.Fields["device"].(string)
			addr, _ := e.Fields["from"].(string)
			if name != "" && addr != "" && !e.Time.IsZero() {
				connects[name] = append(connects[name], sighting{at: e.Time, addr: addr})
			}
		}
	})
	switch {
	case errors.Is(err, logger.ErrJournalDenied):
		res.State, res.Error = "denied", err.Error()
	case err != nil && strings.Contains(err.Error(), "Linux"), err != nil && strings.Contains(err.Error(), "no journalctl"):
		res.State, res.Error = "unavailable", err.Error()
	case err != nil:
		res.State, res.Error = "failed", err.Error()
	}

	for name, seen := range connects {
		client, device := splitDeviceName(name, accounts)
		for _, ev := range sharedFromConnects(client, device, seen) {
			if a, ok := accounts[client+"\x00"+device]; ok {
				ev.AccountID, ev.ClientID = a.ID, a.ClientID
			}
			if created, err := recordSharing(ctx, r.db, ev); err == nil && created {
				res.Shared++
			}
		}
	}
	r.saveImport(res)
	return res
}

func (r *Reconciler) saveImport(res SharingImport) {
	raw, _ := json.Marshal(res)
	if err := database.PutSetting(r.db, KeySharingImport, string(raw)); err != nil {
		r.log.Warn("could not note that the log was read back", "error", err)
	}
}

// accountsByName is every current device by its customer's and its own
// name, which is all the log names them by.
func (r *Reconciler) accountsByName(ctx context.Context) (map[string]model.Account, error) {
	var rows []struct {
		model.Account
		ClientName string
	}
	err := r.db.WithContext(ctx).Table("accounts").
		Select("accounts.*, clients.name AS client_name").
		Joins("JOIN clients ON clients.id = accounts.client_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the devices: %w", err)
	}
	out := make(map[string]model.Account, len(rows))
	for _, row := range rows {
		out[row.ClientName+"\x00"+row.DeviceName] = row.Account
	}
	return out, nil
}

// heldFromLog is a hold the log recorded.
func heldFromLog(e logger.Entry, accounts map[string]model.Account) (model.SharingEvent, bool) {
	client, _ := e.Fields["client"].(string)
	device, _ := e.Fields["device"].(string)
	if client == "" || device == "" || e.Time.IsZero() {
		return model.SharingEvent{}, false
	}
	limit := 0
	if v, _ := e.Fields["limit"].(string); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	place, _ := e.Fields["on"].(string)
	ev := model.SharingEvent{
		Kind:       model.SharingHeld,
		ClientName: client,
		DeviceName: device,
		Limit:      limit,
		Place:      place,
		Source:     model.SharingFromLog,
		StartedAt:  e.Time.UTC(),
		LastSeen:   e.Time.UTC(),
	}
	if a, ok := accounts[client+"\x00"+device]; ok {
		ev.AccountID, ev.ClientID = a.ID, a.ClientID
	}
	return ev, true
}

// sighting is one connection the log recorded.
type sighting struct {
	at   time.Time
	addr string
}

// sharedFromConnects finds, in one device's connections, the times it was
// connected from sharingThreshold addresses or more inside sharingWindow --
// the sharing report's own rule. Times that run into each other are one.
func sharedFromConnects(client, device string, seen []sighting) []model.SharingEvent {
	sort.Slice(seen, func(i, j int) bool { return seen[i].at.Before(seen[j].at) })
	var out []model.SharingEvent
	start := 0
	for end := range seen {
		for seen[end].at.Sub(seen[start].at) > sharingWindow {
			start++
		}
		addrs := map[string]bool{}
		for _, s := range seen[start : end+1] {
			addrs[s.addr] = true
		}
		if len(addrs) < sharingThreshold {
			continue
		}
		list := make([]string, 0, len(addrs))
		for a := range addrs {
			list = append(list, a)
		}
		sort.Strings(list)
		from, to := seen[start].at.UTC(), seen[end].at.UTC()
		if n := len(out); n > 0 && !from.After(out[n-1].LastSeen.Add(historyGap)) {
			out[n-1].LastSeen = to
			out[n-1].Addrs = mergeAddrs(out[n-1].Addrs, strings.Join(list, ","))
			continue
		}
		out = append(out, model.SharingEvent{
			Kind:       model.SharingShared,
			ClientName: client,
			DeviceName: device,
			Addrs:      strings.Join(list, ","),
			Source:     model.SharingFromLog,
			StartedAt:  from,
			LastSeen:   to,
		})
	}
	return out
}

// splitDeviceName splits the "customer / device" the log names a device by.
// Either name can hold " / " itself, so each place it could split is tried
// against the devices there are; for one deleted since, the last is taken.
func splitDeviceName(name string, accounts map[string]model.Account) (client, device string) {
	const sep = " / "
	last := -1
	for i := 0; ; {
		j := strings.Index(name[i:], sep)
		if j < 0 {
			break
		}
		at := i + j
		if _, ok := accounts[name[:at]+"\x00"+name[at+len(sep):]]; ok {
			return name[:at], name[at+len(sep):]
		}
		last = at
		i = at + 1
	}
	if last < 0 {
		return "", name
	}
	return name[:last], name[last+len(sep):]
}
