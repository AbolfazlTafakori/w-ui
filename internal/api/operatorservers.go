package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// The servers a reseller or an admin sells on, as they may see them.
//
// A reseller whose customers cannot connect wants to know whether it is the
// server before they open a ticket, and the interface list they already
// reach is the customer form's picker: it says nothing of whether a tunnel
// is up. This is that one fact and nothing behind it.

// Server states as an operator is told them. Not the panel's own running
// and nodeUp: which node a tunnel is on, and why it is down, are the owner's.
const (
	// serverUp is a tunnel switched on and carrying customers.
	serverUp = "up"
	// serverOff is a tunnel the owner switched off.
	serverOff = "off"
	// serverDown is a tunnel switched on that is not running: its driver did
	// not come up, or the node it is on is not answering.
	serverDown = "down"
)

// operatorServerView is one tunnel as an operator other than the owner is
// shown it. Its own struct, not model.Interface with fields blanked, so a
// field added to the interface later can never reach an operator by being
// forgotten here: keys, addresses, ports, subnets, nodes and the protocol's
// settings are simply not in it.
type operatorServerView struct {
	ID       uint           `json:"id"`
	Name     string         `json:"name"`
	Protocol model.Protocol `json:"protocol"`
	Status   string         `json:"status"`
	// Clients and Online count the asking operator's own customers on it,
	// never the tunnel's whole load: that would be every other operator's
	// customers in aggregate.
	Clients int64 `json:"clients"`
	Online  int64 `json:"online"`
}

func (s *Server) handleOperatorServers(w http.ResponseWriter, r *http.Request) {
	sc := service.ScopeOf(r.Context())
	list, err := s.ifaces.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	counts, err := s.ifaces.OwnCounts(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	nodes := map[uint]model.Node{}
	if all, err := s.nodes.List(r.Context()); err == nil {
		for _, n := range all {
			nodes[n.ID] = n
		}
	}

	out := make([]operatorServerView, 0, len(list))
	for _, f := range list {
		// The tunnels the owner allowed this operator, and no others: a
		// reseller is not told the names of servers they were not given.
		if !sc.Allows(f.ID) {
			continue
		}
		c := counts[f.ID]
		out = append(out, operatorServerView{
			ID: f.ID, Name: f.Name, Protocol: f.Protocol,
			Status:  s.serverStatus(f, nodes),
			Clients: c.Clients, Online: c.Online,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// serverStatus says whether a tunnel is up, switched off, or down, by the
// same facts the interfaces page reads: a tunnel here is running when its
// driver is open, one on another node when that node answers.
func (s *Server) serverStatus(f model.Interface, nodes map[uint]model.Node) string {
	if !f.Enabled {
		return serverOff
	}
	running := false
	if n, ok := nodes[f.NodeID]; ok && n.Kind != model.KindLocal {
		running = n.Reachable
	} else if s.pool != nil {
		_, running = s.pool.Get(f.ID)
	}
	if running {
		return serverUp
	}
	return serverDown
}
