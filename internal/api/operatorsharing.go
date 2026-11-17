package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/scope"
)

// The sharing report, for a reseller or an admin the owner granted it.
//
// Their own customers alone, and their customers' networks rather than their
// addresses. What makes a device look shared is being on several networks at
// once, and a /24 (a /48 for IPv6) says that as well as the address does: a
// phone between wifi and data, or a carrier's NAT, shows as one network, and
// three households as three. The address itself is somebody's home, and is
// the owner's to see, not every reseller's.

// sharingNet is one network a device was seen from, and how many of its
// addresses there were.
type sharingNet struct {
	Net   string `json:"net"`
	Count int    `json:"count"`
}

// operatorSharingView is a device seen from several places now, as an
// operator other than the owner is shown it. Its own struct, not the
// report's, so no field added there reaches them by being forgotten here.
type operatorSharingView struct {
	ClientID   uint         `json:"clientId"`
	ClientName string       `json:"clientName"`
	DeviceName string       `json:"deviceName"`
	Networks   []sharingNet `json:"networks"`
	Since      time.Time    `json:"since"`
}

// operatorSharingEvent is one entry of the record, likewise. Where a hold
// was carried out (the node) and where the entry came from are the owner's.
type operatorSharingEvent struct {
	ID         uint         `json:"id"`
	Kind       string       `json:"kind"`
	ClientID   uint         `json:"clientId"`
	ClientName string       `json:"clientName"`
	DeviceName string       `json:"deviceName"`
	Networks   []sharingNet `json:"networks"`
	Limit      int          `json:"limit"`
	StartedAt  time.Time    `json:"startedAt"`
	LastSeen   time.Time    `json:"lastSeen"`
	Deleted    bool         `json:"deleted"`
}

// sharingCacheFor is how long one reading of the whole report serves every
// operator asking. The report changes on a ten-minute window; a page polling
// every few seconds from each reseller would otherwise rerun the join each
// time for an answer that has not moved.
const sharingCacheFor = 5 * time.Second

type sharingCache struct {
	mu     sync.Mutex
	at     time.Time
	report []reconciler.SharingReport
}

// report is the whole panel's live report, at most sharingCacheFor old.
//
// Read unscoped on purpose: it is one hand-written join over every tunnel,
// which the scope guard rightly refuses for a restricted operator. Nothing
// read here is sent as it is. Every row goes through service.Clients.Own,
// which asks the customers table under the operator's own scope, before any
// of it leaves this file.
func (s *Server) sharingReport(ctx context.Context) ([]reconciler.SharingReport, error) {
	c := &s.sharingCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.report != nil && time.Since(c.at) < sharingCacheFor {
		return c.report, nil
	}
	rep, err := s.rec.Sharing(scope.With(ctx, scope.Scope{}))
	if err != nil {
		return nil, err
	}
	if rep == nil {
		rep = []reconciler.SharingReport{}
	}
	c.report, c.at = rep, time.Now()
	return rep, nil
}

func (s *Server) handleOperatorSharing(w http.ResponseWriter, r *http.Request) {
	out := []operatorSharingView{}
	if s.rec == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rep, err := s.sharingReport(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	ids := make([]uint, 0, len(rep))
	for _, row := range rep {
		ids = append(ids, row.ClientID)
	}
	own, err := s.clients.Own(r.Context(), ids)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	for _, row := range rep {
		if !own[row.ClientID] {
			continue
		}
		out = append(out, operatorSharingView{
			ClientID: row.ClientID, ClientName: row.ClientName, DeviceName: row.DeviceName,
			Networks: networksOf(row.Addrs), Since: row.Since,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOperatorSharingHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// Their own customers' entries, their deleted customers' included: the
	// record keeps whose customer each was (service.SharingRecord).
	events, err := s.clients.SharingRecord(r.Context(), limit)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	ids := make([]uint, 0, len(events))
	for _, ev := range events {
		if ev.ClientID != 0 {
			ids = append(ids, ev.ClientID)
		}
	}
	own, err := s.clients.Own(r.Context(), ids)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	out := make([]operatorSharingEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, operatorSharingEvent{
			ID: ev.ID, Kind: ev.Kind, ClientID: ev.ClientID, ClientName: ev.ClientName,
			DeviceName: ev.DeviceName, Networks: networksOf(strings.Split(ev.Addrs, ",")),
			Limit: ev.Limit, StartedAt: ev.StartedAt, LastSeen: ev.LastSeen,
			// Gone, or no longer theirs: either way not a customer they can open.
			Deleted: !own[ev.ClientID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// networksOf is the networks a list of addresses falls in, each once with
// how many of the addresses were in it, in the order first seen. A port,
// which only tells flows behind a relay apart, is dropped first.
func networksOf(addrs []string) []sharingNet {
	out := []sharingNet{}
	at := map[string]int{}
	for _, a := range addrs {
		n := maskAddr(a)
		if n == "" {
			continue
		}
		if i, ok := at[n]; ok {
			out[i].Count++
			continue
		}
		at[n] = len(out)
		out = append(out, sharingNet{Net: n, Count: 1})
	}
	return out
}

// maskAddr is the network an address is in: its /24 for IPv4, written with
// an x for the last part, and its /48 for IPv6. Anything that does not read
// as an address is left out rather than passed on as it is.
func maskAddr(a string) string {
	a = strings.TrimSpace(a)
	if a == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(a); err == nil {
		a = host
	}
	ip, err := netip.ParseAddr(strings.Trim(a, "[]"))
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	if ip.Is4() {
		b := ip.As4()
		return strconv.Itoa(int(b[0])) + "." + strconv.Itoa(int(b[1])) + "." + strconv.Itoa(int(b[2])) + ".x"
	}
	p, err := ip.Prefix(48)
	if err != nil {
		return ""
	}
	return p.String()
}

// requireGrant refuses an admin or a reseller a page the owner did not tick
// for them. Unlike requireMenu it holds a reseller to it too: sharing is the
// one page a reseller is granted rather than given. The owner, an admin who
// sees every customer, and a machine token are let through as for any route.
func (s *Server) requireGrant(menu string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := adminFrom(r.Context())
		if admin != nil && (admin.Role == model.RoleAdmin || admin.Role == model.RoleReseller) {
			ok, err := s.admins.HasMenu(r.Context(), admin.ID, menu)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "could not check your pages")
				return
			}
			if !ok {
				writeError(w, http.StatusForbidden, "this page is not one of yours")
				return
			}
		}
		next(w, r)
	}
}
