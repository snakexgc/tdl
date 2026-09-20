package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const fieldResult = "result"

func (s *Server) downloadTasksSnapshot(executor string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		items, err := s.downloadControl().Tasks(ctx, executor)
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

func (s *Server) downloadControl() ports.DownloadControl {
	if s.opts.DownloadControl != nil {
		return s.opts.DownloadControl
	}
	return application.DownloadControl(s.downloadAccount(), map[string]ports.DownloadBackend{
		localDownloadExecutor:      s.internalDownloadController(),
		config.DownloaderModeAria2: aria2.NewController(config.From(s.opts.Context), s.opts.NamespaceKV, nil),
	})
}

func (s *Server) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	items, err := s.downloadControl().Tasks(r.Context(), r.URL.Query().Get("executor"))
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
	result, err := s.downloadControl().Control(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(result.Errors) == 0, fieldResult: result})
}
