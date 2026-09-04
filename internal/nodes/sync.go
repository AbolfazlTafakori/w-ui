package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Carrying the panel's decisions out to the servers that terminate tunnels.
//
// The shape is the one Marzban settled on and it is the right one: the panel
// that holds the records is a data plane like any other, with no special case
// for itself. Its own tunnels are programmed by its own reconciler; a remote
// node's tunnels are pushed to the panel running there and programmed by that
// one's reconciler. Usage comes back from both and lands in a single total.
//
// What crosses the wire is desired state, never a command. A node that was
// unreachable for an hour is not owed a replay: the next successful push is the
// whole truth, and its counters kept accumulating in the meantime so nothing
// was lost either.

const (
	// syncTimeout bounds one call to one node. Long enough for a panel with a
	// few thousand peers to accept a push, short enough that an unreachable
	// node does not hold the round open.
	syncTimeout = 30 * time.Second

	// syncInterval is how often the desired state is pushed.
	//
	// Slower than the local reconciler on purpose. This crosses the internet to
	// another machine, and the thing it carries — who is allowed on — changes
	// when a customer runs out, not every two seconds. Usage is drained on the
	// same beat, so a customer's spending on a node reaches their total within
	// one interval.
	syncInterval = 20 * time.Second
)

// Syncer keeps remote nodes matching this panel's records.
type Syncer struct {
	db     *gorm.DB
	log    *slog.Logger
	client *http.Client

	// usage is where drained node counters are handed back to the caller, which
	// folds them into the same per-customer total the local kernel feeds.
	usage func([]service.NodeUsage)

	mu sync.Mutex
	// lastErr is the failure already reported per node, so an unreachable node
	// does not write the same line to the log three times a minute forever.
	lastErr map[uint]string
}

// NewSyncer builds the loop. onUsage is called with whatever a node reports.
func NewSyncer(db *gorm.DB, onUsage func([]service.NodeUsage), log *slog.Logger) *Syncer {
	return &Syncer{
		db:      db,
		log:     log,
		client:  &http.Client{Timeout: syncTimeout},
		usage:   onUsage,
		lastErr: map[uint]string{},
	}
}

// Start runs until ctx is done.
func (s *Syncer) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(syncInterval)
		defer t.Stop()

		// Straight away, so a panel that has just restarted does not leave a
		// node running whatever it had for the first interval.
		s.Round(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Round(ctx)
			}
		}
	}()
}

// Round pushes to every enabled remote node and drains what they counted.
//
// Nodes are done in parallel: one that has gone away must not delay the others,
// and with ten nodes a serial round would spend its whole interval waiting on
// timeouts.
func (s *Syncer) Round(ctx context.Context) {
	var remotes []model.Node
	if err := s.db.WithContext(ctx).
		Where("kind = ? AND enabled = ?", model.KindRemote, true).
		Find(&remotes).Error; err != nil {
		s.log.Error("could not list nodes to sync", "error", err)
		return
	}
	if len(remotes) == 0 {
		return
	}

	var wg sync.WaitGroup
	for i := range remotes {
		node := remotes[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.one(ctx, node)
		}()
	}
	wg.Wait()
}

func (s *Syncer) one(ctx context.Context, node model.Node) {
	states, err := s.desired(ctx, node.ID)
	if err != nil {
		s.report(node, fmt.Errorf("could not build the state for it: %w", err))
		return
	}

	for _, state := range states {
		if err := s.post(ctx, node, "/api/node/sync", state, nil); err != nil {
			s.report(node, fmt.Errorf("pushing %s: %w", state.Interface.Name, err))
			return
		}
	}

	// Drained after the push, so a customer disabled in this round stops before
	// their last bytes are counted rather than after.
	var reply struct {
		Usage []service.NodeUsage `json:"usage"`
	}
	if err := s.post(ctx, node, "/api/node/usage", nil, &reply); err != nil {
		s.report(node, fmt.Errorf("reading its usage: %w", err))
		return
	}
	if len(reply.Usage) > 0 && s.usage != nil {
		s.usage(reply.Usage)
	}
	s.report(node, nil)
}

// desired builds one payload per tunnel assigned to this node.
//
// Per tunnel rather than one big payload: a node with three interfaces should
// not have all three refused because one of them has a customer with a bad row,
// and a failure that names the tunnel is one an operator can act on.
func (s *Syncer) desired(ctx context.Context, nodeID uint) ([]service.NodeState, error) {
	db := s.db.WithContext(ctx)

	var interfaces []model.Interface
	if err := db.Where("node_id = ?", nodeID).Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("load interfaces: %w", err)
	}
	if len(interfaces) == 0 {
		return nil, nil
	}

	ids := make([]uint, 0, len(interfaces))
	for _, iface := range interfaces {
		ids = append(ids, iface.ID)
	}

	var accounts []model.Account
	if err := db.Where("interface_id IN ?", ids).Find(&accounts).Error; err != nil {
		return nil, fmt.Errorf("load accounts: %w", err)
	}

	clientIDs := make([]uint, 0, len(accounts))
	seen := map[uint]bool{}
	for _, a := range accounts {
		if !seen[a.ClientID] {
			seen[a.ClientID] = true
			clientIDs = append(clientIDs, a.ClientID)
		}
	}

	clients := map[uint]model.Client{}
	if len(clientIDs) > 0 {
		var rows []model.Client
		if err := db.Where("id IN ?", clientIDs).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("load clients: %w", err)
		}
		for _, c := range rows {
			clients[c.ID] = c
		}
	}

	out := make([]service.NodeState, 0, len(interfaces))
	for _, iface := range interfaces {
		state := service.NodeState{Interface: interfaceState(iface)}

		grouped := map[uint][]service.NodeAccount{}
		for _, a := range accounts {
			if a.InterfaceID != iface.ID {
				continue
			}
			grouped[a.ClientID] = append(grouped[a.ClientID], service.NodeAccount{
				OriginID:     a.ID,
				DeviceName:   a.DeviceName,
				IP:           a.IP,
				Enabled:      a.Enabled,
				PrivateKey:   a.PrivateKey,
				PublicKey:    a.PublicKey,
				PresharedKey: a.PresharedKey,
				Username:     a.Username,
				Secret:       a.Secret,
			})
		}

		for clientID, accs := range grouped {
			c, ok := clients[clientID]
			if !ok {
				continue
			}
			// The one decision a node is given. Everything behind it — the
			// allowance, the date, an operator's switch — was resolved here,
			// because the allowance is one number spent across every node and
			// only this panel can see the whole of it.
			state.Clients = append(state.Clients, service.NodeClient{
				OriginID:       c.ID,
				Enabled:        c.Status == model.StatusActive,
				RateBitsPerSec: c.RateBitsPerSec,
				Accounts:       accs,
			})
		}
		out = append(out, state)
	}
	return out, nil
}

func interfaceState(iface model.Interface) service.NodeInterface {
	out := service.NodeInterface{
		OriginID:     iface.ID,
		Name:         iface.Name,
		Protocol:     iface.Protocol,
		Enabled:      iface.Enabled,
		ListenPort:   iface.ListenPort,
		Subnet:       iface.Subnet,
		EndpointHost: iface.EndpointHost,
		MTU:          iface.MTU,
		DNS:          iface.DNS,
		NATInterface: iface.NATInterface,
		Mode:         iface.Mode,
		// The server's own key material. It has to go: the node is the machine
		// that terminates the tunnel, and a customer's configuration names this
		// public key. Generating a second one there would hand every customer a
		// file for a server that does not exist.
		PrivateKey: iface.PrivateKey,
		PublicKey:  iface.PublicKey,
	}
	if awg := iface.AWG.V; awg != (model.AWGParams{}) {
		v := awg
		out.AWG = &v
	}
	if ovpn := iface.OpenVPN.V; ovpn.CACert != "" {
		v := ovpn
		out.OpenVPN = &v
	}
	return out
}

func (s *Syncer) post(ctx context.Context, node model.Node, path string, body, into any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	endpoint := strings.TrimRight(node.Address, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+node.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("the token was refused; issue a new one on that panel")
	case resp.StatusCode == http.StatusNotFound:
		// The likeliest cause by far, and worth naming rather than reporting a
		// bare 404 an operator would go looking for in their own logs.
		return fmt.Errorf("it has no node endpoint; that panel is older than this one")
	case resp.StatusCode >= 300:
		return fmt.Errorf("it answered %s", resp.Status)
	}

	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			return fmt.Errorf("it answered, but not like a W-UI panel")
		}
	}
	return nil
}

// report logs a node's state only when it changes.
func (s *Syncer) report(node model.Node, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}

	s.mu.Lock()
	previous := s.lastErr[node.ID]
	s.lastErr[node.ID] = msg
	s.mu.Unlock()

	if msg == previous {
		return
	}
	if err != nil {
		s.log.Warn("node is not in step with this panel", "node", node.Name, "error", err)
		return
	}
	s.log.Info("node is in step with this panel", "node", node.Name)
}
