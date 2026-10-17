package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// A round against a node of another panel -- 3x-ui, Pasarguard -- whose
// inbounds this panel holds as tunnels and whose users are this panel's
// files. What every such panel shares is here: its inbounds read and held,
// the files on them gone through one by one, what is ours there and no one's
// here taken away, what each spent charged, what failed said. What differs
// -- how a file is a user there, how it is counted, what else a round puts
// there -- is the panel's own (xrayPanel), in xui.go and pg.go.

// xrayLinksEvery is how many rounds go by between rereading every file's
// links regardless: two hours. They change when the inbound does -- its port,
// its address, its Reality keys, its host -- and that is seen every round by
// the inbound's fingerprint (inboundPrint), so a file's links are read again
// the round its inbound changes; this is only the net under that.
const xrayLinksEvery = 360

// inboundPrint is a fingerprint of what of an inbound goes into its links:
// all of it but its clients, which change with every customer and not the
// links of the rest.
func inboundPrint(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		var v any
		switch x := p.(type) {
		case json.RawMessage:
			_ = objectOrString(x, &v)
		default:
			v = x
		}
		if m, ok := v.(map[string]any); ok {
			delete(m, "clients")
			if set, ok := m["settings"].(map[string]any); ok {
				delete(set, "clients")
			}
		}
		b, _ := json.Marshal(v) // a map's keys in order, so the same inbound prints the same
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// changedInbounds is which of a node's inbounds, by their id there, print
// otherwise than the round before; none on the first round, which rereads
// everything anyway.
func (s *Syncer) changedInbounds(nodeID uint, prints map[int]string) map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inboundPrints == nil {
		s.inboundPrints = map[uint]map[int]string{}
	}
	before, had := s.inboundPrints[nodeID]
	s.inboundPrints[nodeID] = prints
	changed := map[int]bool{}
	if !had {
		return changed
	}
	for id, p := range prints {
		if before[id] != p {
			changed[id] = true
		}
	}
	return changed
}

// xrayPanel is one node of another panel, for one round.
type xrayPanel interface {
	// read is what it holds now: its inbounds, as this panel holds them, with
	// each one's fingerprint by its id there (inboundPrint), and whatever of
	// its users the round needs, kept for the calls after.
	read(ctx context.Context) ([]service.XUIInboundSeen, map[int]string, error)
	// prepare is told the files on it before they are gone through.
	prepare(ctx context.Context, files []xrayFile) error
	// own puts one of this panel's files there as it should be; adopt does
	// the same for a user brought in from that panel.
	own(ctx context.Context, f xrayFile) xrayResult
	adopt(ctx context.Context, f xrayFile, marks map[uint]adoptedMark) xrayResult
	// sweep takes away what is this panel's there and no file's here, wanted
	// being the names of those that are.
	sweep(ctx context.Context, wanted map[string]bool) (changed bool, failures []string)
	// finish is what else a round does there once the files are done.
	finish(ctx context.Context, touched bool, files []xrayFile) []string
}

// xrayFile is one file of this panel's on a node, with what decides it.
type xrayFile struct {
	acc    model.Account
	iface  model.Interface
	client model.Client
	// on is whether it is to be served: the customer, the file and the
	// tunnel all on, and the node within its own allowance.
	on bool
	// links is whether its links are to be read again this round.
	links bool
}

// xrayResult is what became of a file there.
type xrayResult struct {
	name    string // its name there, when it is one this panel made
	usage   *service.XUIUsage
	link    string // its links, when they are new
	changed bool
	err     error
}

// xrayRound runs one round against a node of another panel.
func (s *Syncer) xrayRound(ctx context.Context, node model.Node, p xrayPanel) error {
	if s.XUIMirror == nil {
		return fmt.Errorf("this panel cannot hold another panel's inbounds")
	}
	seen, prints, err := p.read(ctx)
	if err != nil {
		return err
	}
	reprinted := s.changedInbounds(node.ID, prints)
	db := s.db.WithContext(ctx)
	// What the tunnels were before this read, so a file whose inbound
	// changed there -- its port, its keys -- is given its new links now
	// rather than at the next periodic reread.
	var before []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", node.ID, model.ProtocolXray).Find(&before).Error; err != nil {
		return fmt.Errorf("load its tunnels: %w", err)
	}
	was := make(map[uint]model.Interface, len(before))
	for _, f := range before {
		was[f.ID] = f
	}
	if err := s.XUIMirror(ctx, node.ID, xuiHost(node.Address), seen); err != nil {
		return err
	}
	var ifaces []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", node.ID, model.ProtocolXray).Find(&ifaces).Error; err != nil {
		return fmt.Errorf("load its tunnels: %w", err)
	}
	byID := make(map[uint]model.Interface, len(ifaces))
	ids := make([]uint, 0, len(ifaces))
	moved := map[uint]bool{}
	for _, f := range ifaces {
		byID[f.ID] = f
		ids = append(ids, f.ID)
		if w, ok := was[f.ID]; ok && (w.ListenPort != f.ListenPort || w.EndpointHost != f.EndpointHost || xuiShape(w.XUI.V) != xuiShape(f.XUI.V)) {
			moved[f.ID] = true
		}
		if reprinted[f.XUI.V.RemoteID] {
			moved[f.ID] = true
		}
	}
	var accounts []model.Account
	if len(ids) > 0 {
		if err := db.Where("interface_id IN ?", ids).Find(&accounts).Error; err != nil {
			return fmt.Errorf("load accounts: %w", err)
		}
	}
	clients := map[uint]model.Client{}
	if len(accounts) > 0 {
		cids := make([]uint, 0, len(accounts))
		for _, a := range accounts {
			cids = append(cids, a.ClientID)
		}
		var rows []model.Client
		if err := db.Where("id IN ?", cids).Find(&rows).Error; err != nil {
			return fmt.Errorf("load clients: %w", err)
		}
		for _, c := range rows {
			clients[c.ID] = c
		}
	}

	s.mu.Lock()
	if s.xuiRounds == nil {
		s.xuiRounds = map[uint]int{}
	}
	s.xuiRounds[node.ID]++
	reread := s.xuiRounds[node.ID]%xrayLinksEvery == 1
	s.mu.Unlock()

	files := make([]xrayFile, 0, len(accounts))
	for _, acc := range accounts {
		iface, ok := byID[acc.InterfaceID]
		if !ok || iface.XUI.V.Gone || !iface.XUI.V.Customers {
			continue
		}
		c, ok := clients[acc.ClientID]
		if !ok {
			continue
		}
		files = append(files, xrayFile{
			acc: acc, iface: iface, client: c,
			on:    !node.OverAllowance && c.Status.Serviceable() && acc.Enabled && iface.Enabled,
			links: reread || moved[acc.InterfaceID] || acc.XUILink == "",
		})
	}
	if err := p.prepare(ctx, files); err != nil {
		return err
	}

	var failures []string
	touched := false
	marks := map[uint]adoptedMark{}
	wanted := map[string]bool{}
	var usage []service.XUIUsage
	links := map[uint]string{}
	for _, f := range files {
		var r xrayResult
		if f.acc.Adopted != "" {
			r = p.adopt(ctx, f, marks)
		} else {
			r = p.own(ctx, f)
		}
		if r.name != "" {
			wanted[r.name] = true
		}
		if r.err != nil {
			failures = append(failures, r.err.Error())
		}
		if r.usage != nil {
			usage = append(usage, *r.usage)
		}
		if r.link != "" {
			links[f.acc.ID] = r.link
		}
		touched = touched || r.changed
	}
	changed, swept := p.sweep(ctx, wanted)
	touched = touched || changed
	failures = append(failures, swept...)

	if err := service.SaveXUICounters(ctx, s.db, usage, links); err != nil {
		return fmt.Errorf("could not keep its counters: %w", err)
	}
	s.chargeXUI(ctx, node, usage)
	if err := s.saveMarks(ctx, marks); err != nil {
		failures = append(failures, err.Error())
	}
	failures = append(p.finish(ctx, touched, files), failures...)

	if len(failures) > 0 {
		if len(failures) > 3 {
			failures = append(failures[:3], fmt.Sprintf("and %d more", len(failures)-3))
		}
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

// xuiShape is what of an inbound goes into its links; its counters and
// client count change every round and do not.
func xuiShape(in model.XUIInbound) model.XUIInbound {
	in.Clients, in.Up, in.Down = 0, 0, 0
	return in
}
