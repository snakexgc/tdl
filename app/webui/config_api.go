package webui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const shutdownInProgressMessage = "an update, reboot or reset is already in progress"

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.configuration.Read(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config":            value,
			"active_config":     s.activeConfiguration,
			"restart_available": s.opts.RequestReboot != nil,
			"editable":          s.opts.ConfigurationManager != nil,
		})
	case http.MethodPatch:
		var req struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
			return
		}
		next, err := s.configuration.Patch(r.Context(), req.Values)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ports.ErrConfigurationConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err)
			return
		}
		slog.Info("运行设置已保存", "component", "panel.webui", "account", s.namespace())
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                true,
			"config":            next,
			"active_config":     s.activeConfiguration,
			"restart_available": s.opts.RequestReboot != nil,
			"editable":          s.opts.ConfigurationManager != nil,
			fieldMessage:        "设置已保存到 tdl_config.json，重启后生效。",
		})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	info, err := s.checkUpdate(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "update": info})
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestUpdate == nil {
		writeError(w, http.StatusBadRequest, errors.New("update is not available in this mode"))
		return
	}
	if !s.shutdownRequested.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, errors.New(shutdownInProgressMessage))
		return
	}
	plan, info, err := s.downloadUpdate(r)
	if err != nil {
		s.shutdownRequested.Store(false)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"update":     info,
		fieldMessage: fmt.Sprintf("更新包已下载，准备更新到 %s 并重启。", info.LatestVersion),
	})
	_ = http.NewResponseController(w).Flush()
	s.opts.RequestUpdate(plan)
}

func (s *Server) checkUpdate(r *http.Request) (types.UpdateInfo, error) {
	if s.opts.Updater != nil {
		return s.opts.Updater.Check(r.Context())
	}
	return types.UpdateInfo{}, errors.New("update service is unavailable")
}

func (s *Server) downloadUpdate(r *http.Request) (types.UpdatePlan, types.UpdateInfo, error) {
	if s.opts.Updater != nil {
		return s.opts.Updater.Download(r.Context())
	}
	return types.UpdatePlan{}, types.UpdateInfo{}, errors.New("update service is unavailable")
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestReboot == nil {
		writeError(w, http.StatusBadRequest, errors.New("reboot is not available in this mode"))
		return
	}
	if !s.shutdownRequested.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, errors.New(shutdownInProgressMessage))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldMessage: "正在重启 tdl"})
	_ = http.NewResponseController(w).Flush()
	s.opts.RequestReboot()
}
