package fwsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Firewall is one of the server's firewalls, on.
type Firewall interface {
	Name() string
	// Allow lets r in; added says whether it did, or whether a rule letting
	// r in was there already -- not the helper's, then, and never taken
	// away by it.
	Allow(r Rule) (added bool, err error)
	// Has says whether r is let in.
	Has(r Rule) (bool, error)
	// Remove takes the helper's rule for r away.
	Remove(r Rule) error
}

// RecordDir is where what the helper opened is kept: root's alone.
var RecordDir = "/var/lib/wui-firewall"

func recordFile() string { return filepath.Join(RecordDir, "opened.json") }

// record is, per firewall, what the helper opened.
type record map[string][]Rule

func loadRecord() (record, error) {
	b, err := os.ReadFile(recordFile())
	if errors.Is(err, fs.ErrNotExist) {
		return record{}, nil
	}
	if err != nil {
		return nil, err
	}
	rec := record{}
	if err := json.Unmarshal(b, &rec); err != nil {
		// A record that cannot be read is no reason to close anything: the
		// worst it costs is a rule left open.
		return record{}, nil
	}
	return rec, nil
}

func (rec record) save() error {
	if err := os.MkdirAll(RecordDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(RecordDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := recordFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, recordFile())
}

// Sync makes each firewall that is on let in what is wanted: what it lacks
// is added, and what the helper added before and is no longer wanted is
// taken away. Every firewall is tried; the errors of all are returned.
func Sync(want []Rule, firewalls []Firewall, log *slog.Logger) error {
	want = normalize(want)
	rec, err := loadRecord()
	if err != nil {
		return err
	}
	wanted := map[Rule]bool{}
	for _, r := range want {
		wanted[r] = true
	}
	var errs []error
	for _, fw := range firewalls {
		var ours []Rule
		for _, r := range rec[fw.Name()] {
			if wanted[r] {
				ours = append(ours, r)
				continue
			}
			if err := fw.Remove(r); err != nil {
				errs = append(errs, fmt.Errorf("%s: closing %s: %w", fw.Name(), r, err))
				ours = append(ours, r) // still ours; tried again next time
				continue
			}
			log.Info("closed a port no tunnel uses", "firewall", fw.Name(), "rule", r.String())
		}
		mine := map[Rule]bool{}
		for _, r := range ours {
			mine[r] = true
		}
		for _, r := range want {
			if mine[r] {
				// The helper's: put back if something took it away.
				if ok, err := fw.Has(r); err == nil && !ok {
					if _, err := fw.Allow(r); err != nil {
						errs = append(errs, fmt.Errorf("%s: opening %s: %w", fw.Name(), r, err))
					}
				}
				continue
			}
			added, err := fw.Allow(r)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: opening %s: %w", fw.Name(), r, err))
				continue
			}
			if added {
				ours = append(ours, r)
				log.Info("opened a tunnel's port", "firewall", fw.Name(), "rule", r.String())
			}
		}
		rec[fw.Name()] = normalize(ours)
	}
	if err := rec.save(); err != nil {
		errs = append(errs, fmt.Errorf("keeping the record: %w", err))
	}
	return errors.Join(errs...)
}
