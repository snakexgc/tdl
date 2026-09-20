package webui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/app/updater"
	panel "github.com/snakexgc/tdl/application/panel.webui"
	"github.com/snakexgc/tdl/pkg/config"
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
		writeJSON(w, http.StatusOK, map[string]any{"config": value, "component_managed": s.componentConfigurationOwned()})
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
			writeError(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("运行设置已保存", "component", "panel.webui", "account", s.namespace())
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                true,
			"config":            publicConfig(next),
			"component_managed": s.componentConfigurationOwned(),
			fieldMessage:        "配置已保存。模块开关会立即生效；监听地址、命名空间、Bot Token 等基础连接参数建议重启后再使用。",
		})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if s.opts.ModuleManager == nil {
		writeError(w, http.StatusBadRequest, errors.New("module manager is not available"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"modules": s.opts.ModuleManager.ModuleStates()})
	case http.MethodPost:
		var req struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
			return
		}
		state, err := s.opts.ModuleManager.SetModuleEnabled(r.Context(), req.ID, req.Enabled)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("模块启停设置已保存", "component", state.ComponentID, "account", s.namespace(), "enabled", req.Enabled)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"module":  state,
			"modules": s.opts.ModuleManager.ModuleStates(),
		})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	info, err := s.checkUpdate(r)
	if err != nil {
		slog.Warn("检查软件更新失败", "component", "update.self", "account", s.namespace(), "error", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	slog.Info("软件更新检查已完成", "component", "update.self", "account", s.namespace())
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

func (s *Server) checkUpdate(r *http.Request) (updater.Info, error) {
	if s.opts.Updater != nil {
		return s.opts.Updater.Check(r.Context())
	}
	return updater.CheckLatest(r.Context(), config.EffectiveProxy(config.From(s.opts.Context)))
}

func (s *Server) downloadUpdate(r *http.Request) (updater.Plan, updater.Info, error) {
	if s.opts.Updater != nil {
		return s.opts.Updater.Download(r.Context())
	}
	return updater.DownloadLatest(r.Context(), config.EffectiveProxy(config.From(s.opts.Context)))
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

func publicConfig(cfg *config.Config) *config.Config { return panel.PublicConfig(cfg) }
func isBlankSensitivePatch(path string, raw json.RawMessage) bool {
	return panel.IsBlankSensitivePatch(path, raw)
}

func (s *Server) componentConfigurationOwned() bool {
	if s.opts.ComponentManager == nil {
		return false
	}
	_, enabled := s.opts.ComponentManager.ComponentConfigurations()
	return enabled
}
