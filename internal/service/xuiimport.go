package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Bringing the users a 3x-ui or Pasarguard node already has in as this
// panel's customers.
//
// A user is taken over where it is, not made again: its client or user
// there, its credentials, its links and the subscription its owner already
// handed out all stay as they are, and the customer's subscription here
// carries the same links beside whatever else they are sold. What it had
// spent, its allowance and its expiry come over with it; from then on they
// are this panel's, and its own limits there are cleared, so that panel
// never ends it on numbers this one no longer keeps. Users made there later
// are brought in by asking again; nothing is taken over unasked.

// AdoptedAccount is one user there, taken over by one file here: who it is
// there, its credential, its counters as they stood, and whether it was on.
type AdoptedAccount struct {
	Remote string
	Secret string
	Up     uint64
	Down   uint64
	On     bool
}

// ImportFile is one file of an imported customer: the tunnel it is on here
// and the user there it takes over.
type ImportFile struct {
	InterfaceID uint
	AdoptedAccount
}

// ImportCandidate is a user on a node that can be brought in: one customer
// here, with a file on every inbound it has there.
type ImportCandidate struct {
	// Key names it among the node's users: a 3x-ui subscription id (or an
	// email, where it has none), a Pasarguard username.
	Key          string     `json:"key"`
	Name         string     `json:"name"`
	Note         string     `json:"note,omitempty"`
	Enabled      bool       `json:"enabled"`
	QuotaBytes   uint64     `json:"quotaBytes"`
	UsedBytes    uint64     `json:"usedBytes"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	DurationDays int        `json:"durationDays,omitempty"` // the clock starts on first use
	DeviceLimit  int        `json:"deviceLimit"`
	Inbounds     []string   `json:"inbounds"`
	// TelegramID is the account its owner gave it there, for the bot here
	// to know them by.
	TelegramID int64 `json:"-"`
	// Imported says it is a customer here already; Reason why it cannot be.
	Imported bool         `json:"imported"`
	Reason   string       `json:"reason,omitempty"`
	Files    []ImportFile `json:"-"`
}

// SetRemote gives the customers' service the panels used as nodes, whose
// users it brings in.
func (s *Clients) SetRemote(r RemotePanels) { s.remote = r }

// importing keeps imports one at a time (ImportNodeUsers).
var importing sync.Mutex

// ImportResult is what became of one user asked for.
type ImportResult struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	ClientID uint   `json:"clientId,omitempty"`
	Error    string `json:"error,omitempty"`
}

// NodeUsers is every user a W-UI, 3x-ui or Pasarguard node has of its own,
// as candidates, the ones already brought in marked. A W-UI node's are only
// those it still has: one brought in is this panel's on the node too.
func (s *Clients) NodeUsers(ctx context.Context, nodeID uint) ([]ImportCandidate, error) {
	if s.remote == nil {
		return nil, fmt.Errorf("%w: this panel cannot read another panel's users", ErrInvalid)
	}
	db := s.db.WithContext(ctx)
	var node model.Node
	if err := db.First(&node, nodeID).Error; err != nil {
		return nil, fmt.Errorf("%w: no node %d", ErrNotFound, nodeID)
	}
	if node.Kind == model.KindRemote {
		return s.wuiCandidates(ctx, node)
	}
	if node.Kind != model.KindXUI && node.Kind != model.KindPG {
		return nil, fmt.Errorf("%w: users are brought in only from a W-UI, 3x-ui or Pasarguard node", ErrInvalid)
	}
	var tunnels []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", node.ID, model.ProtocolXray).Find(&tunnels).Error; err != nil {
		return nil, err
	}
	cands, err := s.remote.ImportCandidates(ctx, node, tunnels)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(tunnels))
	for _, t := range tunnels {
		ids = append(ids, t.ID)
	}
	taken := map[string]bool{}
	if len(ids) > 0 {
		var accs []model.Account
		if err := db.Select("adopted").Where("interface_id IN ? AND adopted <> ''", ids).Find(&accs).Error; err != nil {
			return nil, err
		}
		for _, a := range accs {
			taken[a.Adopted] = true
		}
	}
	for i := range cands {
		for _, f := range cands[i].Files {
			if taken[f.Remote] {
				cands[i].Imported = true
			}
		}
	}
	return cands, nil
}

// ImportNodeUsers brings in the users asked for by key, or every one not
// yet brought in when none is named.
func (s *Clients) ImportNodeUsers(ctx context.Context, nodeID uint, keys []string) ([]ImportResult, error) {
	// One at a time: two asked at once -- a button pressed twice -- would
	// each find a user not yet brought in, and bring it in twice.
	importing.Lock()
	defer importing.Unlock()
	if s.remote != nil {
		var node model.Node
		if err := s.db.WithContext(ctx).First(&node, nodeID).Error; err != nil {
			return nil, fmt.Errorf("%w: no node %d", ErrNotFound, nodeID)
		}
		if node.Kind == model.KindRemote {
			return s.importWUI(ctx, node, keys)
		}
	}
	cands, err := s.NodeUsers(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, k := range keys {
		asked[k] = true
	}
	var out []ImportResult
	for _, c := range cands {
		if len(keys) > 0 && !asked[c.Key] {
			continue
		}
		res := ImportResult{Key: c.Key, Name: c.Name}
		switch {
		case c.Imported:
			if len(keys) == 0 {
				continue
			}
			res.Error = "already a customer here"
		case c.Reason != "":
			res.Error = c.Reason
		default:
			made, err := s.Create(ctx, importInput(c))
			if err != nil {
				res.Error = err.Error()
			} else {
				res.ClientID = made.ID
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// importInput is a candidate as a new customer: one file on each of its
// inbounds, each taking its user there over.
func importInput(c ImportCandidate) CreateInput {
	enabled := c.Enabled
	in := CreateInput{
		Name:        importName(c.Name, c.Key),
		Note:        c.Note,
		QuotaBytes:  c.QuotaBytes,
		UsedBytes:   c.UsedBytes,
		ExpiresAt:   c.ExpiresAt,
		DeviceLimit: c.DeviceLimit,
		TelegramID:  c.TelegramID,
		Enabled:     &enabled,
		Historical:  true,
		DeviceNames: []string{"device-1"},
		Adopt:       map[uint]AdoptedAccount{},
	}
	if c.DurationDays > 0 && c.ExpiresAt == nil {
		in.StartOnFirstUse, in.DurationDays = true, c.DurationDays
	}
	for _, f := range c.Files {
		in.InterfaceIDs = append(in.InterfaceIDs, f.InterfaceID)
		in.Adopt[f.InterfaceID] = f.AdoptedAccount
	}
	return in
}

// importName is a user's name as a customer's, within what a name may be.
func importName(name, key string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = key
	}
	if r := []rune(name); len(r) > maxClientName {
		name = string(r[:maxClientName])
	}
	return name
}
