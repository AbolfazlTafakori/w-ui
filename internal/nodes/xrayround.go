package nodes

import (
	"context"
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
// links, which change when the inbound does: its port, its address, its
// Reality keys.
const xrayLinksEvery = 15

// xrayPanel is one node of another panel, for one round.
type xrayPanel interface {
	// read is what it holds now: its inbounds, as this panel holds them, and
	// whatever of its users the round needs, kept for the calls after.
	read(ctx context.Context) ([]service.XUIInboundSeen, error)
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
	seen, err := p.read(ctx)
	if err != nil {
		return err
	}
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
