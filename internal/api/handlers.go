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

	// Which server this tunnel runs on, and whether that server is answering.
	//
	// The id alone tells an operator nothing, and a tunnel on a node that has
	// gone quiet looks identical to one that is working: the panel holds the
	// records either way, and the customers on it are the ones who find out.
	NodeName  string `json:"nodeName"`
	NodeLocal bool   `json:"nodeLocal"`
	NodeUp    bool   `json:"nodeUp"`

	// Running is whether a driver is actually open for this tunnel, which is a
	// different question from whether the row is enabled. A tunnel switched on
	// whose port was taken, or whose tool is missing, is enabled and carrying
	// nobody — and telling those apart is the whole of "is this server up".
	Running bool `json:"running"`
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

	// One query for the names rather than one per row.
	nodesByID := map[uint]model.Node{}
	if all, err := s.nodes.List(r.Context()); err == nil {
		for _, n := range all {
			nodesByID[n.ID] = n
		}
	}

	out := make([]interfaceView, 0, len(list))
	for i := range list {
		v := interfaceView{Interface: redactKeys(list[i])}
		if n, ok := nodesByID[list[i].NodeID]; ok {
			v.NodeName = n.Name
			v.NodeLocal = n.Kind == model.KindLocal
			// The local node is up by definition: this code is running on it.
			v.NodeUp = v.NodeLocal || n.Reachable
		}
		if u, err := s.ifaces.PoolUsage(list[i].ID); err == nil {
			v.Allocated, v.Capacity = u.Allocated, u.Capacity
		}
		if l, ok := loads[list[i].ID]; ok {
			v.Clients, v.Devices, v.UsedBytes = l.Clients, l.Devices, l.UsedBytes
		}
		// A tunnel on another node is that node's to run, and this panel has no
		// driver for it: reported as running when the node itself is answering,
		// which is as much as can be known from here.
		if v.NodeLocal || v.NodeName == "" {
			_, v.Running = s.pool.Get(list[i].ID)
		} else {
			v.Running = v.NodeUp && list[i].Enabled
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

	// Opened now rather than at the next tick, so the operator finds out here
	// whether the port was free and the tool was installed. A failure is
	// reported alongside the interface rather than instead of it: the row is
	// created either way, and the reconciler keeps retrying.
	out := map[string]any{"interface": redactKeys(*iface)}
	// Only a tunnel on this machine. One on another node has its kernel
	// somewhere else: opening a driver here would build a device nobody dials,
	// and its failure to open would be reported as a fault on a server that is
	// working. The sync loop carries it to the node that owns it instead.
	if s.pool != nil && iface.NodeID == s.localNodeID {
		if err := s.pool.Open(r.Context(), iface); err != nil {
			s.log.Warn("a new interface would not come up",
				"interface", iface.Name, "error", err)
			out["warning"] = humanMessage(err)
		}
	}
	writeJSON(w, http.StatusCreated, out)
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

// handleInterfaceProfile hands over the one OpenVPN file a tunnel has.
//
// The same file for everybody on it, on purpose: an OpenVPN profile carries
// nothing about who is connecting. An operator gives this out once and then
// sells access by creating credentials, which is also what makes revoking work
// — the customer's file keeps opening and connects to nothing.
func (s *Server) handleInterfaceProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	name, body, err := s.ifaces.Profile(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// handleClientConfigs hands an operator every configuration one customer has.
//
// An archive by default, because a .conf file holds exactly one [Interface]:
// a customer with two devices cannot be given one file, and joining them —
// which is what the subscription formats do — quietly delivers only the first.
func (s *Server) handleClientConfigs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "zip"
	}

	bundle, err := s.subs.BundleForClient(r.Context(), id, format)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", bundle.ContentType)
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", bundle.Filename))
	// These are keys. Nothing between here and the operator should keep a copy.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle.Body)
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
	q := r.URL.Query()

	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	level, search := q.Get("level"), q.Get("q")

	// The journal is the same panel's lines, kept by the system rather than by
	// this process — which is the only place they survive a restart, and a
	// restart is usually what an operator is asking about.
	if q.Get("source") == "journal" {
		entries, err := logger.Journal(r.Context(), limit, level, search)
		if err != nil {
			// Not a failure of the request. The page asked for a source this
			// server cannot offer, and should say so rather than show nothing.
			writeJSON(w, http.StatusOK, map[string]any{
				"entries": []logger.Entry{},
				"source":  "journal",
				"notice":  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "source": "journal"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": logger.Recent.Recent(limit, level, search),
		"source":  "panel",
	})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	g, err := s.clients.CreateGroup(r.Context(), in.Name, in.Note)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	ungrouped, err := s.clients.DeleteGroup(r.Context(), in.Name)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ungrouped": ungrouped})
}

// --- nodes ------------------------------------------------------------

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var in service.NodeInput
	if !decode(w, r, &in) {
		return
	}
	node, err := s.nodes.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// Probed straight away, so an operator finds out about a wrong address or a
	// refused token now rather than in half a minute.
	if s.prober != nil {
		s.prober.ProbeOne(r.Context(), *node)
		_ = s.db.WithContext(r.Context()).First(node, node.ID).Error
	}
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.NodeInput
	if !decode(w, r, &in) {
		return
	}
	node, err := s.nodes.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if s.prober != nil {
		s.prober.ProbeOne(r.Context(), *node)
		_ = s.db.WithContext(r.Context()).First(node, node.ID).Error
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.nodes.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProbeNode asks one node now, for the button next to it.
func (s *Server) handleProbeNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var node model.Node
	if err := s.db.WithContext(r.Context()).First(&node, id).Error; err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if s.prober == nil {
		fail(w, s.log, fmt.Errorf("probing is not available"))
		return
	}

	s.prober.ProbeOne(r.Context(), node)
	_ = s.db.WithContext(r.Context()).First(&node, id).Error
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.ListTokens(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	issued, err := s.nodes.IssueToken(r.Context(), in.Name)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// The only time the secret exists outside the caller's hands. It is stored
	// as a hash, so this response is the one chance to copy it.
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.nodes.RevokeToken(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRestartInterface reopens one interface's driver.
//
// The reconciler heals most things on its own, but not a driver whose Open
// failed at startup — a tunnel whose port was taken, or whose tool was not yet
// installed. Without this the only way out is restarting the whole panel, which
// disconnects every customer on every other interface to fix one.
func (s *Server) handleRestartInterface(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var iface model.Interface
	if err := s.db.WithContext(r.Context()).First(&iface, id).Error; err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Through the pool, so the reopened driver is the one the reconciler will
	// use. Building a driver here and keeping it to ourselves is what the old
	// version did, and it left the interface looking restarted while nothing
	// was actually pushing peers to it.
	if err := s.pool.Open(r.Context(), &iface); err != nil {
		// Reported rather than hidden: the reason a tunnel will not come up is
		// the whole content of this request.
		s.log.Warn("interface restart failed", "interface", iface.Name, "error", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"interface": iface.Name,
			"error":     humanMessage(err),
		})
		return
	}

	s.log.Info("interface restarted", "interface", iface.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "interface": iface.Name})
}
