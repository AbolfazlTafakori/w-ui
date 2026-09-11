package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// ── outbound subscriptions ───────────────────────────────────────────────────

func (s *Server) handleListOutboundSubs(w http.ResponseWriter, r *http.Request) {
	list, err := s.obSubs.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateOutboundSub(w http.ResponseWriter, r *http.Request) {
	var in service.OutboundSubInput
	if !decode(w, r, &in) {
		return
	}
	sub, err := s.obSubs.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (s *Server) handleUpdateOutboundSub(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.OutboundSubInput
	if !decode(w, r, &in) {
		return
	}
	sub, err := s.obSubs.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleDeleteOutboundSub(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.obSubs.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderOutboundSubs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uint `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.obSubs.Reorder(r.Context(), in.IDs); err != nil {
		fail(w, s.log, err)
		return
	}
	list, err := s.obSubs.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePreviewOutboundSub(w http.ResponseWriter, r *http.Request) {
	var in service.OutboundSubInput
	if !decode(w, r, &in) {
		return
	}
	items, err := s.obSubs.Preview(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// Passwords are not for the preview table.
	for i := range items {
		items[i].Password = ""
		items[i].PrivateKey = ""
		items[i].PresharedKey = ""
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRefreshOutboundSub(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.obSubs.Refresh(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	list, err := s.obSubs.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRefreshAllOutboundSubs(w http.ResponseWriter, r *http.Request) {
	if err := s.obSubs.RefreshAll(r.Context()); err != nil {
		fail(w, s.log, err)
		return
	}
	list, err := s.obSubs.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ── WARP ─────────────────────────────────────────────────────────────────────

func (s *Server) handleWarpState(w http.ResponseWriter, r *http.Request) {
	st, err := s.providers.WarpState(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleWarpRegister(w http.ResponseWriter, r *http.Request) {
	st, err := s.providers.WarpRegister(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleWarpLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		License string `json:"license"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.providers.WarpSetLicense(r.Context(), in.License)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleWarpDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.providers.WarpDelete(r.Context()); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWarpChangeIP(w http.ResponseWriter, r *http.Request) {
	st, err := s.providers.WarpChangeIP(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleWarpOutbound(w http.ResponseWriter, r *http.Request) {
	ob, err := s.providers.WarpAddOutbound(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, ob)
}

// ── NordVPN ──────────────────────────────────────────────────────────────────

func (s *Server) handleNordState(w http.ResponseWriter, r *http.Request) {
	st, err := s.providers.NordState(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleNordLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.providers.NordLogin(r.Context(), in.Token)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleNordSetKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PrivateKey string `json:"privateKey"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.providers.NordSetKey(r.Context(), in.PrivateKey)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleNordLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.providers.NordLogout(r.Context()); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNordCountries(w http.ResponseWriter, r *http.Request) {
	list, err := s.providers.NordCountries(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNordServers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CountryID int `json:"countryId"`
	}
	if !decode(w, r, &in) {
		return
	}
	list, err := s.providers.NordServers(r.Context(), in.CountryID)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNordOutbound(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname  string `json:"hostname"`
		PublicKey string `json:"publicKey"`
	}
	if !decode(w, r, &in) {
		return
	}
	ob, renewed, err := s.providers.NordAddOutbound(r.Context(), in.Hostname, in.PublicKey)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outbound": ob, "renewed": renewed})
}

// ── PIA ──────────────────────────────────────────────────────────────────────

func (s *Server) handlePIAState(w http.ResponseWriter, r *http.Request) {
	st, err := s.providers.PIAState(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handlePIALogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.providers.PIALogin(r.Context(), in.Username, in.Password)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handlePIALogout(w http.ResponseWriter, r *http.Request) {
	if err := s.providers.PIALogout(r.Context()); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePIARegions(w http.ResponseWriter, r *http.Request) {
	list, err := s.providers.PIARegions(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePIAOutbound(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RegionID string `json:"regionId"`
		Hostname string `json:"hostname"`
	}
	if !decode(w, r, &in) {
		return
	}
	ob, renewed, err := s.providers.PIAAddOutbound(r.Context(), in.RegionID, in.Hostname)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outbound": ob, "renewed": renewed})
}

// ── keys ─────────────────────────────────────────────────────────────────────

// handleWGKey hands the outbound form a key pair, or the public half of a
// private key it already has. The private key is echoed back only when it
// was generated here; one typed in is never repeated by the server.
func (s *Server) handleWGKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PrivateKey string `json:"privateKey"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.PrivateKey) == "" {
		pair, err := wgkey.NewPair()
		if err != nil {
			fail(w, s.log, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"privateKey": pair.Private.String(),
			"publicKey":  pair.Public.String(),
		})
		return
	}
	k, err := wgkey.Parse(strings.TrimSpace(in.PrivateKey))
	if err != nil {
		fail(w, s.log, fmt.Errorf("%w: that is not a WireGuard key", service.ErrInvalid))
		return
	}
	pub, err := k.Public()
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": pub.String()})
}

// ── apply ────────────────────────────────────────────────────────────────────

// handleApplyRouting pushes the outbounds and rules into the kernel now, the
// job the reconciler otherwise does on its next tick.
func (s *Server) handleApplyRouting(w http.ResponseWriter, r *http.Request) {
	if s.rec == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.rec.ApplyRouting(r.Context()); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
