package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *Server) handleComponentHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	manager, ok := s.opts.ComponentManager.(ComponentDiagnostics)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("component diagnostics are unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": manager.ComponentHealth()})
}

func (s *Server) handleComponents(w http.ResponseWriter, r *http.Request) {
	if s.opts.ComponentManager == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("component host is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, editable := s.opts.ComponentManager.ComponentConfigurations()
		writeJSON(w, http.StatusOK, map[string]any{"components": items, "editable": editable})
	case http.MethodPatch:
		var request struct {
			ID     string         `json:"id"`
			Values map[string]any `json:"values"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeError(w, http.StatusBadRequest, fmt.Errorf("expected one configuration object"))
			return
		}
		if err := s.opts.ComponentManager.SaveComponentConfiguration(r.Context(), request.ID, request.Values); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		items, editable := s.opts.ComponentManager.ComponentConfigurations()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "components": items, "editable": editable})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
}
