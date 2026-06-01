package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-faster/errors"

	appforward "github.com/iyear/tdl/app/forward"
)

// handleForwards lists the persistent forward queue (pending, running and
// recently finished jobs) and the count still in operation.
func (s *Server) handleForwards(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	queue := appforward.Jobs()
	items, err := queue.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	running, err := queue.RunningCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		fieldItems:   items,
		fieldRunning: running,
	})
}

// handleForwardActions applies bulk pause/resume/delete to forward jobs.
func (s *Server) handleForwardActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	queue := appforward.Jobs()
	var (
		result appforward.ActionResult
		err    error
	)
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case actionPause:
		result, err = queue.Pause(r.Context(), req.IDs)
	case actionResume, "start", "retry":
		result, err = queue.Resume(r.Context(), req.IDs)
	case actionDelete:
		result, err = queue.Delete(r.Context(), req.IDs)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", req.Action))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(result.Errors) == 0,
		"result": result,
	})
}
