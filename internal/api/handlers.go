package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/service"
)

// metaResponse tells the frontend what this server can actually do, so the UI
// offers only protocols that have a driver and warns when quota enforcement is
// not running.
type metaResponse struct {
	Version            string           `json:"version"`
	Protocols          []model.Protocol `json:"protocols"`
	Locales            []string         `json:"locales"`
	DefaultLocale      string           `json:"defaultLocale"`
	EnforcementActive  bool             `json:"enforcementActive"`
	EnforcementMessage string           `json:"enforcementMessage,omitempty"`
	ShapingActive      bool             `json:"shapingActive"`
	ShapingMessage     string           `json:"shapingMessage,omitempty"`
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	resp := metaResponse{
		Version:       s.version,
		Protocols:     backend.Registered(),
		Locales:       s.catalog.Locales(),
		DefaultLocale: i18n.DefaultLocale,
	}
	if err := s.enforcer.Health(r.Context()); err != nil {
		resp.EnforcementMessage = err.Error()
	} else {
		resp.EnforcementActive = true
	}
	if s.shaper != nil {
		if err := s.shaper.Health(r.Context()); err != nil {
			resp.ShapingMessage = err.Error()
		} else {
			resp.ShapingActive = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMessages serves a locale merged over English, so the frontend never has
// to deal with a missing key.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	locale := r.PathValue("locale")
	writeJSON(w, http.StatusOK, map[string]any{
		"locale":    locale,
		"direction": i18n.DirectionOf(locale),
		"messages":  s.catalog.Messages(locale),
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.clients.Overview(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// --- interfaces -------------------------------------------------------------

// interfaceView pairs an interface with what it is carrying: how full its
// address pool is, and how many customers and how much traffic sit on it.
type interfaceView struct {
	model.Interface
	Allocated int    `json:"allocated"`
	Capacity  int    `json:"capacity"`
	Clients   int64  `json:"clients"`
	Devices   int64  `json:"devices"`
	UsedBytes uint64 `json:"usedBytes"`
}

// redactKeys strips the server's own key material from an interface before it
// leaves this process.
//
// The OpenVPN parameters are stored as JSON, so the struct that holds the
// server private key and the tls-crypt key is the same one the database column
// is built from — tagging those fields json:"-" would stop them being saved at
// all. They are removed here instead, on the way out.
//
// This matters more than it looks. Whoever holds the server key can present
// themselves as this VPN server to every customer on it, and a response body
// travels through browser memory, proxy logs and error reporting on its way to
// a page that never needed it.
func redactKeys(iface model.Interface) model.Interface {
	p := iface.OpenVPN.V
	configured := p.ServerKey != "" && p.CACert != ""

	p.ServerKey = ""
	p.TLSCryptKey = ""
	p.ServerCert = ""
	// The certificate authority stays: it is public by design, it is what every
	// customer's own configuration already carries, and the panel shows it.
	iface.OpenVPN = model.JSON(p)
	iface.Configured = configured

	// The WireGuard private key is already hidden by its struct tag; cleared
	// here as well so this function is the one place to look.
	iface.PrivateKey = ""
	return iface
}

// interfaceViews builds the list once so both the interfaces page and the
// overview report the same numbers.
func (s *Server) interfaceViews(r *http.Request) ([]interfaceView, error) {
	list, err := s.ifaces.List(r.Context())
	if err != nil {
		return nil, err
	}
	loads, err := s.ifaces.Loads(r.Context())
	if err != nil {
		return nil, err
	}

	out := make([]interfaceView, 0, len(list))
	for i := range list {
		v := interfaceView{Interface: redactKeys(list[i])}
		if u, err := s.ifaces.PoolUsage(list[i].ID); err == nil {
			v.Allocated, v.Capacity = u.Allocated, u.Capacity
		}
		if l, ok := loads[list[i].ID]; ok {
			v.Clients, v.Devices, v.UsedBytes = l.Clients, l.Devices, l.UsedBytes
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) handleListInterfaces(w http.ResponseWriter, r *http.Request) {
	out, err := s.interfaceViews(r)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateInterface(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.UpdateInterfaceInput
	if !decode(w, r, &in) {
		return
	}
	iface, err := s.ifaces.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, redactKeys(*iface))
}

func (s *Server) handleDeleteInterface(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.ifaces.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreateInterface(w http.ResponseWriter, r *http.Request) {
	var in service.CreateInterfaceInput
	if !decode(w, r, &in) {
		return
	}
	iface, err := s.ifaces.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, redactKeys(*iface))
}

// --- clients ----------------------------------------------------------

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))

	res, err := s.clients.List(r.Context(), service.ListFilter{
		Search:   q.Get("search"),
		Status:   model.ClientStatus(q.Get("status")),
		Protocol: model.Protocol(q.Get("protocol")),
		Group:    q.Get("group"),
		Sort:     q.Get("sort"),
		Page:     page,
		PerPage:  perPage,
	})
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var in service.CreateInput
	if !decode(w, r, &in) {
		return
	}
	client, err := s.clients.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, client)
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	client, err := s.clients.Get(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.UpdateInput
	if !decode(w, r, &in) {
		return
	}
	client, err := s.clients.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.clients.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetTraffic(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	client, err := s.clients.ResetTraffic(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

// handleBulk applies one action to a selected set of clients.
func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		IDs    []uint `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}

	n, err := s.clients.Bulk(r.Context(), service.BulkAction(body.Action), body.IDs)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

// --- devices ----------------------------------------------------------------

func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	acc, err := s.clients.AddDevice(r.Context(), id, body.Name)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, acc)
}

func (s *Server) handleRemoveDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.clients.RemoveDevice(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProfile returns a device's configuration, either as JSON for the UI to
// display and turn into a QR code, or as a file download.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	profile, err := s.clients.Profile(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	if r.URL.Query().Get("download") != "1" {
		writeJSON(w, http.StatusOK, profile)
		return
	}
	w.Header().Set("Content-Type", profile.MIMEType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", profile.Filename))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(profile.Body)); err != nil {
		s.log.Error("write profile", "error", err)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid id %q", raw))
		return 0, false
	}
	return uint(id), true
}

// --- groups -----------------------------------------------------------------

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	res, err := s.clients.Groups(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGroupNames(w http.ResponseWriter, r *http.Request) {
	names, err := s.clients.ListGroupNames(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := s.clients.RenameGroup(r.Context(), body.From, body.To)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handleAssignGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Group string `json:"group"`
		IDs   []uint `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := s.clients.AssignGroup(r.Context(), body.Group, body.IDs)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handleGroupAction(w http.ResponseWriter, r *http.Request) {
	var op service.GroupOp
	if !decode(w, r, &op) {
		return
	}
	n, err := s.clients.ApplyToGroup(r.Context(), op)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

// --- client bulk operations -------------------------------------------------

func (s *Server) handleAdjust(w http.ResponseWriter, r *http.Request) {
	var in service.AdjustInput
	if !decode(w, r, &in) {
		return
	}
	n, err := s.clients.Adjust(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handleResetAll(w http.ResponseWriter, r *http.Request) {
	n, err := s.clients.ResetAllTraffic(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := s.clients.DeleteByStatus(r.Context(), model.ClientStatus(body.Status))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var in service.BatchInput
	if !decode(w, r, &in) {
		return
	}
	created, err := s.clients.CreateBatch(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"created": len(created),
		"clients": created,
	})
}

// handleExport writes every client as JSON for the operator to keep.
//
// Credentials are left out: this is a record of who was sold what, not a
// portable copy of the tunnels, and an export that leaks keys is a liability
// sitting in someone's downloads folder.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	page, err := s.clients.List(r.Context(), service.ListFilter{PerPage: 200, Page: 1})
	if err != nil {
		fail(w, s.log, err)
		return
	}

	all := make([]model.Client, 0, page.Total)
	all = append(all, page.Items...)
	for p := 2; int64(len(all)) < page.Total; p++ {
		next, err := s.clients.List(r.Context(), service.ListFilter{PerPage: 200, Page: p})
		if err != nil {
			fail(w, s.log, err)
			return
		}
		if len(next.Items) == 0 {
			break
		}
		all = append(all, next.Items...)
	}

	// Written in the shape import reads, so the file this produces is one the
	// panel can take back.
	records := make([]service.ClientRecord, 0, len(all))
	for _, c := range all {
		records = append(records, service.ToRecord(c))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q",
			"wui-clients-"+time.Now().Format("2006-01-02")+".json"))
	writeJSON(w, http.StatusOK, map[string]any{
		"exportedAt": time.Now().UTC(),
		"count":      len(records),
		"clients":    records,
	})
}

// handleImport loads a client list produced by the export.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var in service.ImportInput
	if !decode(w, r, &in) {
		return
	}

	report, err := s.clients.Import(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Info("client list imported",
		"created", report.Created, "replaced", report.Replaced,
		"skipped", report.Skipped, "failed", report.Failed)
	writeJSON(w, http.StatusOK, report)
}

// handleLogs serves the panel's recent log.
//
// Diagnosing a customer's problem otherwise means an SSH session and
// journalctl, on a different machine from the panel already open in front of
// whoever is looking.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": logger.Recent.Recent(limit, r.URL.Query().Get("level")),
	})
}
