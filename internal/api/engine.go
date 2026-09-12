package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/service"
)

type engineResponse struct {
	Settings     service.EngineSettings `json:"settings"`
	Defaults     service.EngineSettings `json:"defaults"`
	DNSListening []string               `json:"dnsListening"`
}

func (s *Server) engineResponse(r *http.Request, cfg service.EngineSettings) engineResponse {
	return engineResponse{Settings: cfg, Defaults: s.engine.Defaults(), DNSListening: s.engine.DNSListening()}
}

func (s *Server) handleGetEngine(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.engine.Get(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, s.engineResponse(r, cfg))
}

func (s *Server) handleSaveEngine(w http.ResponseWriter, r *http.Request) {
	var in service.EngineSettings
	if !decode(w, r, &in) {
		return
	}
	saved, err := s.engine.Save(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, s.engineResponse(r, saved))
}

func (s *Server) handleResetEngine(w http.ResponseWriter, r *http.Request) {
	saved, err := s.engine.Reset(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, s.engineResponse(r, saved))
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	doc, err := s.template.Export(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleApplyTemplate(w http.ResponseWriter, r *http.Request) {
	var in service.TemplateDoc
	if !decode(w, r, &in) {
		return
	}
	section := r.URL.Query().Get("section")
	if section == "" {
		section = "complete"
	}
	res, err := s.template.Apply(r.Context(), in, section)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
