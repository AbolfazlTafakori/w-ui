package reconciler

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Detecting a shared credential.
//
// Once two devices hold the same key the tunnel cannot tell them apart — that
// is what a key is. The only signal left is where the packets come from: one
// customer is one place at a time, two customers are usually two. So the panel
// records the public addresses an account connects from and reports when several
// are live at once.
//
// This is evidence, not a verdict. A phone moving between wifi and mobile data
// changes address legitimately, and a carrier-grade NAT can put two customers
// behind one address. Nothing is disconnected automatically; the operator is
// shown the addresses and decides.

const (
	// sharingWindow is how recently an address must have been used to count as
	// live. Long enough that a customer's two devices are both seen, short
	// enough that this morning's café does not still count this evening.
	sharingWindow = 10 * time.Minute

	// endpointRetention is how long an address is kept once it stops being
	// used. It bounds the table on a busy server and gives an operator enough
	// history to see a pattern.
	endpointRetention = 30 * 24 * time.Hour

	// sharingThreshold is how many live addresses make a credential suspect.
	// Two is normal — a phone hopping networks reaches it — so the report
	// starts at three.
	sharingThreshold = 3

	// pruneEvery bounds how often the old rows are swept. The sweep is a
	// delete over an indexed column, but it has no business running on every
	// two-second tick.
	pruneEvery = time.Hour
)

// SharingReport is one account seen from several places at once.
type SharingReport struct {
	AccountID  uint      `json:"accountId"`
	ClientID   uint      `json:"clientId"`
	ClientName string    `json:"clientName"`
	DeviceName string    `json:"deviceName"`
	Addrs      []string  `json:"addrs"`
	Since      time.Time `json:"since"`
}

// recordEndpoints stores where accounts were seen, in one statement.
//
// The address is reduced to its host: a customer's source port changes on every
// reconnect, so keeping it would turn one sharer into a hundred.
func recordEndpoints(ctx context.Context, db *gorm.DB, seen map[uint]string, clientOf map[uint]uint, now time.Time) error {
	if len(seen) == 0 {
		return nil
	}

	relays := relayHosts(seen, clientOf)
	rows := make([]model.AccountEndpoint, 0, len(seen))
	for accountID, endpoint := range seen {
		host := hostOf(endpoint)
		if relays[host] {
			// Behind a relay every customer arrives from the relay's own
			// address, and only the port tells their flows apart.
			host = strings.TrimSpace(endpoint)
		}
		if host == "" {
			continue
		}
		rows = append(rows, model.AccountEndpoint{
			AccountID: accountID,
			Addr:      host,
			FirstSeen: now,
			LastSeen:  now,
			Hits:      1,
		})
	}
	if len(rows) == 0 {
		return nil
	}

	// Sorted so concurrent writers touch the rows in the same order, which is
	// what keeps SQLite from deadlocking two batches against each other.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].AccountID != rows[j].AccountID {
			return rows[i].AccountID < rows[j].AccountID
		}
		return rows[i].Addr < rows[j].Addr
	})

	err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "addr"}},
		// FirstSeen is deliberately not touched: it is the answer to "since
		// when", and an upsert that refreshed it would erase that.
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen": now,
			"hits":      gorm.Expr("hits + 1"),
		}),
	}).Create(&rows).Error
	if err != nil {
		return fmt.Errorf("record endpoints: %w", err)
	}
	return nil
}

// relayMinClients is how many different customers one address must be
// carrying at once before it is taken for a relay rather than a household.
// A family behind one router is two or three of a panel's customers; a
// relay in front of the server is all of them.
const relayMinClients = 5

// relayHosts names the addresses that are relays: whatever this server's
// traffic arrives through -- a forwarder on another machine, a tunnel
// endpoint, a load balancer -- shows as one address carrying many
// customers at once. Recognised by that rather than configured, so a
// panel works the same behind any relay, or none.
func relayHosts(seen map[uint]string, clientOf map[uint]uint) map[string]bool {
	clients := map[string]map[uint]bool{}
	for accountID, endpoint := range seen {
		host := hostOf(endpoint)
		if host == "" {
			continue
		}
		if clients[host] == nil {
			clients[host] = map[uint]bool{}
		}
		clients[host][clientOf[accountID]] = true
	}
	out := map[string]bool{}
	for host, c := range clients {
		if len(c) >= relayMinClients {
			out[host] = true
		}
	}
	return out
}

// hostOf strips the port from an endpoint.
func hostOf(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		// A relay on this same machine hands every customer's packets on
		// from loopback, and only the port tells the flows apart; the port
		// is kept then, so the sharing report still has something to say.
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return endpoint
		}
		return host
	}
	// Already a bare address, which is what the WireGuard driver reports when
	// the peer has no port yet.
	return endpoint
}

// Sharing lists accounts currently reachable from several places.
func (r *Reconciler) Sharing(ctx context.Context) ([]SharingReport, error) {
	cutoff := time.Now().UTC().Add(-sharingWindow)

	var rows []struct {
		AccountID  uint
		ClientID   uint
		ClientName string
		DeviceName string
		Addr       string
		FirstSeen  time.Time
		LastSeen   time.Time
	}

	// Raw SQL because this is a three-way join with a window filter, and
	// expressing it through the ORM would be longer and no clearer.
	err := r.db.WithContext(ctx).Raw(`
		SELECT e.account_id   AS account_id,
		       a.client_id    AS client_id,
		       c.name         AS client_name,
		       a.device_name  AS device_name,
		       e.addr         AS addr,
		       e.first_seen   AS first_seen,
		       e.last_seen    AS last_seen
		FROM account_endpoints e
		JOIN accounts a ON a.id = e.account_id
		JOIN clients  c ON c.id = a.client_id
		WHERE e.last_seen >= ?
		  AND e.account_id IN (
		      SELECT account_id FROM account_endpoints
		      WHERE last_seen >= ?
		      GROUP BY account_id
		      HAVING COUNT(DISTINCT addr) >= ?
		  )
		ORDER BY e.account_id, e.addr`,
		cutoff, cutoff, sharingThreshold).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("sharing report: %w", err)
	}

	// Behind a relay the addresses are one host and many ports, and a port
	// is a flow, not a device: every reconnect -- a phone waking, a walk
	// from wifi to data -- is a new one. Three ports in ten minutes is one
	// device three times over unless the flows were alive together. So
	// for those the report counts only flows that overlapped in time, and
	// says nothing of an account whose flows never did.
	spans := map[uint][]span{}
	for _, row := range rows {
		spans[row.AccountID] = append(spans[row.AccountID], span{row.Addr, row.FirstSeen, row.LastSeen})
	}
	byAccount := map[uint]*SharingReport{}
	var order []uint
	for _, row := range rows {
		if ports := spans[row.AccountID]; sameHost(ports) && concurrent(ports) < sharingThreshold {
			continue
		}
		rep, ok := byAccount[row.AccountID]
		if !ok {
			rep = &SharingReport{
				AccountID:  row.AccountID,
				ClientID:   row.ClientID,
				ClientName: row.ClientName,
				DeviceName: row.DeviceName,
				Since:      row.FirstSeen,
			}
			byAccount[row.AccountID] = rep
			order = append(order, row.AccountID)
		}
		rep.Addrs = append(rep.Addrs, row.Addr)
		if row.FirstSeen.Before(rep.Since) {
			rep.Since = row.FirstSeen
		}
	}

	out := make([]SharingReport, 0, len(order))
	for _, id := range order {
		out = append(out, *byAccount[id])
	}
	return out, nil
}

// span is when one address was in use.
type span struct {
	addr        string
	first, last time.Time
}

// sameHost reports whether every address is the same host on different
// ports -- what a relay in front of the server looks like.
func sameHost(spans []span) bool {
	if len(spans) < 2 {
		return false
	}
	host := ""
	for _, s := range spans {
		h, _, err := net.SplitHostPort(s.addr)
		if err != nil {
			return false
		}
		if host == "" {
			host = h
		} else if h != host {
			return false
		}
	}
	return true
}

// concurrent is the most addresses that were in use at one moment: a
// sweep over their first and last sightings.
func concurrent(spans []span) int {
	type edge struct {
		at    time.Time
		delta int
	}
	var edges []edge
	for _, s := range spans {
		edges = append(edges, edge{s.first, +1}, edge{s.last, -1})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta // an end before a start at the same instant
		}
		return edges[i].at.Before(edges[j].at)
	})
	n, most := 0, 0
	for _, e := range edges {
		n += e.delta
		if n > most {
			most = n
		}
	}
	return most
}

// pruneEndpoints drops addresses nobody has used for a long time.
func pruneEndpoints(ctx context.Context, db *gorm.DB, now time.Time) error {
	cutoff := now.Add(-endpointRetention)
	err := db.WithContext(ctx).
		Where("last_seen < ?", cutoff).
		Delete(&model.AccountEndpoint{}).Error
	if err != nil {
		return fmt.Errorf("prune endpoints: %w", err)
	}
	return nil
}
