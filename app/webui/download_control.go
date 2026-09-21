package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const fieldResult = "result"

func (s *Server) downloadTasksSnapshot(executor string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		if executor == config.DownloadExecutorAria2 && !config.Aria2Enabled(config.From(s.opts.Context)) {
			return map[string]any{fieldItems: []types.DownloadTask{}}, nil
		}
		if s.opts.DownloadControl == nil {
			return nil, fmt.Errorf("download control is unavailable")
		}
		items, err := s.opts.DownloadControl.Tasks(ctx, executor)
		if err != nil {
			return nil, err
		}
		return map[string]any{fieldItems: items}, nil
	}
}

func (s *Server) downloadAccount() types.AccountID {
	account := types.AccountID(s.opts.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	return account
}

func (s *Server) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if r.URL.Query().Get("executor") == config.DownloadExecutorAria2 && !config.Aria2Enabled(config.From(s.opts.Context)) {
		writeJSON(w, http.StatusOK, map[string]any{fieldItems: []types.DownloadTask{}})
		return
	}
	if s.opts.DownloadControl == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("download control is unavailable"))
		return
	}
	items, err := s.opts.DownloadControl.Tasks(r.Context(), r.URL.Query().Get("executor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{fieldItems: items})
}

func (s *Server) handleDownloadTaskActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var request types.DownloadAction
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("expected one action object"))
		return
	}
	if request.Account != "" && request.Account != s.downloadAccount() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("download account mismatch"))
		return
	}
	request.Account = s.downloadAccount()
	if request.Executor == config.DownloadExecutorAria2 && !config.Aria2Enabled(config.From(s.opts.Context)) {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("aria2 is not the active downloader"))
		return
	}
	if s.opts.DownloadControl == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("download control is unavailable"))
		return
	}
	result, err := s.opts.DownloadControl.Control(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(result.Errors) == 0, fieldResult: result})
}
