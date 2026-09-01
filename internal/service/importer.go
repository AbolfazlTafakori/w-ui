package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Importing a client list.
//
// This is the other half of the export: moving a customer list to a new server,
// or putting one back after a mistake. It deliberately does not restore keys —
// the export does not carry them, and a customer list is not a backup. Restoring
// a whole server is what the backup archive is for.
//
// Because keys are not carried, every imported client is issued new ones and
// their existing configuration stops working. That is stated plainly rather than
// discovered.

// ImportInput is a client list as the export writes it.
type ImportInput struct {
	Clients []ClientRecord `json:"clients"`

	// InterfaceID is where the imported clients are placed. The export does not
	// record it, because the interface on the old server has no meaning on the
	// new one.
	InterfaceID uint `json:"interfaceId"`

	// OnConflict decides what happens to a name that already exists.
	OnConflict ConflictPolicy `json:"onConflict"`
}

// ClientRecord is one row of the list.
//
// Export writes this shape and import reads it, so the round trip is guaranteed
// by there being one definition rather than two that have to be kept in step.
// The panel's own export failing to import is not a hypothetical: it is what
// happened when the export wrote whole database rows instead.
type ClientRecord struct {
	Name           string     `json:"name"`
	Note           string     `json:"note"`
	Group          string     `json:"group"`
	QuotaBytes     uint64     `json:"quotaBytes"`
	UsedBytes      uint64     `json:"usedBytes"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	DeviceLimit    int        `json:"deviceLimit"`
	RateBitsPerSec uint64     `json:"rateBitsPerSec"`
	ResetCycle     string     `json:"resetCycle"`
	Status         string     `json:"status"`

	StartOnFirstUse bool `json:"startOnFirstUse"`
	DurationDays    int  `json:"durationDays"`
}

// ConflictPolicy is what to do about a name that is already taken.
type ConflictPolicy string

const (
	// ConflictSkip leaves the existing client alone. It is the default because
	// it is the only one that cannot destroy something already being paid for.
	ConflictSkip ConflictPolicy = "skip"
	// ConflictRename imports alongside, with a suffix.
	ConflictRename ConflictPolicy = "rename"
	// ConflictReplace overwrites the existing client's plan, keeping its keys
	// so the customer's configuration keeps working.
	ConflictReplace ConflictPolicy = "replace"
)

// ImportReport says what happened, per outcome.
type ImportReport struct {
	Created  int      `json:"created"`
	Replaced int      `json:"replaced"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Problems []string `json:"problems,omitempty"`
}

// Import loads a client list.
func (s *Clients) Import(ctx context.Context, in ImportInput) (*ImportReport, error) {
	if len(in.Clients) == 0 {
		return nil, fmt.Errorf("%w: the file contains no clients", ErrInvalid)
	}
	if in.OnConflict == "" {
		in.OnConflict = ConflictSkip
	}
	switch in.OnConflict {
	case ConflictSkip, ConflictRename, ConflictReplace:
	default:
		return nil, fmt.Errorf("%w: unknown conflict policy %q", ErrInvalid, in.OnConflict)
	}

	iface, err := s.loadInterface(ctx, in.InterfaceID)
	if err != nil {
		return nil, err
	}

	// Existing names are read once. Doing it per row would be a query per
	// client, which on a list of a few thousand is the difference between a
	// second and a minute.
	var existing []model.Client
	if err := s.db.WithContext(ctx).Select("id", "name").Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("service: read existing clients: %w", err)
	}
	byName := make(map[string]uint, len(existing))
	for _, c := range existing {
		byName[strings.ToLower(strings.TrimSpace(c.Name))] = c.ID
	}

	report := &ImportReport{}
	for _, row := range in.Clients {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			report.Failed++
			report.Problems = append(report.Problems, "a row has no name")
			continue
		}

		id, clashes := byName[strings.ToLower(name)]
		if clashes {
			switch in.OnConflict {
			case ConflictSkip:
				report.Skipped++
				continue
			case ConflictReplace:
				if err := s.replaceFromImport(ctx, id, row); err != nil {
					report.Failed++
					report.Problems = append(report.Problems,
						fmt.Sprintf("%s: %v", name, err))
					continue
				}
				report.Replaced++
				continue
			case ConflictRename:
				name = uniqueName(name, byName)
			}
		}

		created, err := s.Create(ctx, importToCreate(row, name, iface.ID))
		if err != nil {
			report.Failed++
			report.Problems = append(report.Problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		byName[strings.ToLower(name)] = created.ID
		report.Created++
	}

	// A wall of identical failures helps nobody; the first few say what is
	// wrong and the count says how widespread it is.
	if len(report.Problems) > 10 {
		report.Problems = append(report.Problems[:10],
			fmt.Sprintf("and %d more", len(report.Problems)-10))
	}
	return report, nil
}

func importToCreate(row ClientRecord, name string, ifaceID uint) CreateInput {
	deviceLimit := row.DeviceLimit
	if deviceLimit < 1 {
		deviceLimit = 1
	}
	cycle := model.ResetCycle(row.ResetCycle)
	switch cycle {
	case model.ResetNone, model.ResetDaily, model.ResetWeekly, model.ResetMonthly:
	default:
		cycle = model.ResetNone
	}

	return CreateInput{
		Name:            name,
		Note:            row.Note,
		Group:           row.Group,
		InterfaceID:     ifaceID,
		QuotaBytes:      row.QuotaBytes,
		ExpiresAt:       row.ExpiresAt,
		DeviceLimit:     deviceLimit,
		RateBitsPerSec:  row.RateBitsPerSec,
		ResetCycle:      cycle,
		StartOnFirstUse: row.StartOnFirstUse,
		DurationDays:    row.DurationDays,
	}
}

// replaceFromImport updates an existing client's plan and leaves its devices
// alone, so the configuration the customer already holds keeps working.
func (s *Clients) replaceFromImport(ctx context.Context, id uint, row ClientRecord) error {
	updates := map[string]any{
		"note":              row.Note,
		"group":             row.Group,
		"quota_bytes":       row.QuotaBytes,
		"expires_at":        row.ExpiresAt,
		"rate_bits_per_sec": row.RateBitsPerSec,
		"updated_at":        time.Now().UTC(),
	}
	if row.DeviceLimit >= 1 {
		updates["device_limit"] = row.DeviceLimit
	}
	// Usage is deliberately not restored. The number on the old server counted
	// traffic this one never carried, and importing it would cut a customer off
	// for data they have not used here.

	err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("id = ?", id).Updates(updates).Error
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// uniqueName finds a free name by suffixing.
func uniqueName(base string, taken map[string]uint) string {
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if _, clash := taken[strings.ToLower(candidate)]; !clash {
			return candidate
		}
	}
	return fmt.Sprintf("%s (%d)", base, time.Now().UnixNano())
}

// ToRecord projects a stored client into the exchange shape.
//
// Identifiers, usage counters and keys are all left out. An export is a record
// of who was sold what, to be carried to another server or read by a human —
// not a copy of this server's state, and not something that should leak a key
// into a downloads folder.
func ToRecord(c model.Client) ClientRecord {
	return ClientRecord{
		Name:            c.Name,
		Note:            c.Note,
		Group:           c.Group,
		QuotaBytes:      c.QuotaBytes,
		UsedBytes:       c.UsedBytes,
		ExpiresAt:       c.ExpiresAt,
		DeviceLimit:     c.DeviceLimit,
		RateBitsPerSec:  c.RateBitsPerSec,
		ResetCycle:      string(c.ResetCycle),
		Status:          string(c.Status),
		StartOnFirstUse: c.StartOnFirstUse,
		DurationDays:    c.DurationDays,
	}
}
