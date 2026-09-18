package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func (s *Server) componentAction(route types.WebRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resolver, ok := s.opts.ComponentManager.(interface {
			ResolveComponentPort(string, string) (any, error)
		})
		if !ok {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("component action is unavailable"))
			return
		}
		value, err := resolver.ResolveComponentPort(route.Owner, route.Port)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
		action, ok := value.(ports.WebAction)
		if !ok {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("invalid component action port"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || (len(body) > 0 && !json.Valid(body)) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("expected a JSON control message of at most 1 MiB"))
			return
		}
		result, err := action.Handle(r.Context(), types.WebRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if result.Status == 0 {
			result.Status = http.StatusOK
		}
		if result.Status < 200 || result.Status > 599 {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("invalid component response status"))
			return
		}
		writeJSON(w, result.Status, result.Body)
	}
}
