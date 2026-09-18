package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/snakexgc/tdl/rte"
)

const (
	fieldComponents = "components"
	fieldEditable   = "editable"
)

type componentToggleManager interface {
	SetComponentEnabled(context.Context, string, bool, string) error
}

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
		if r.Method == http.MethodGet {
			items := rte.NewDirectory(s.opts.Catalog, s.opts.ComponentStore).Configurations(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{fieldComponents: items, fieldEditable: false})
			return
		}
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("component host is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, editable := s.opts.ComponentManager.ComponentConfigurations()
		_, canToggle := s.opts.ComponentManager.(componentToggleManager)
		writeJSON(w, http.StatusOK, map[string]any{fieldComponents: items, fieldEditable: editable, "can_toggle": canToggle && editable})
	case http.MethodPatch:
		var request struct {
			Enabled  *bool          `json:"enabled"`
			Revision string         `json:"revision"`
			ID       string         `json:"id"`
			Values   map[string]any `json:"values"`
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
		var err error
		if request.Enabled != nil {
			if request.Values != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("save values and enablement separately"))
				return
			}
			manager, ok := s.opts.ComponentManager.(componentToggleManager)
			if !ok {
				writeError(w, http.StatusServiceUnavailable, fmt.Errorf("component enablement is unavailable"))
				return
			}
			err = manager.SetComponentEnabled(r.Context(), request.ID, *request.Enabled, request.Revision)
		} else if versioned, ok := s.opts.ComponentManager.(interface {
			SaveComponentConfigurationVersion(context.Context, string, map[string]any, string) error
		}); ok {
			err = versioned.SaveComponentConfigurationVersion(r.Context(), request.ID, request.Values, request.Revision)
		} else {
			err = s.opts.ComponentManager.SaveComponentConfiguration(r.Context(), request.ID, request.Values)
		}
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, rte.ErrConfigurationConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err)
			return
		}
		items, editable := s.opts.ComponentManager.ComponentConfigurations()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldComponents: items, fieldEditable: editable})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
}
