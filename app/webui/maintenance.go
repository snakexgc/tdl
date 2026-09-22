package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func (s *Server) handleStorageClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, errors.New("清理存储需要 application/json 请求。"))
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
		Namespace    string `json:"namespace"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if decoder.Decode(new(any)) != io.EOF || request.Confirmation != "CLEAN_STORAGE" {
		writeError(w, http.StatusBadRequest, errors.New("请先确认存储清理提示。"))
		return
	}
	namespace := s.namespace()
	if request.Namespace != namespace {
		writeError(w, http.StatusConflict, errors.New("当前账号已变化，请刷新页面后重新确认。"))
		return
	}
	if s.opts.KVEngine == nil || s.opts.NamespaceKV == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("当前运行模式不支持存储清理。"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if s.opts.ComponentStore != nil {
		document, err := s.opts.ComponentStore.Load(ctx, ports.KVMaintenanceName)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
		if !document.Enabled {
			writeError(w, http.StatusServiceUnavailable, errors.New("存储维护模块已停用，请先在模块管理中启用。"))
			return
		}
	}
	if !s.maintenanceRunning.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, errors.New("存储清理正在进行，请稍候。"))
		return
	}
	defer s.maintenanceRunning.Store(false)
	account := types.AccountID(namespace)
	host, maintenance, err := application.MaintenanceHost(ctx, account, taskhub.CleanupRepository{
		Engine: s.opts.KVEngine, Namespace: namespace, Store: s.opts.NamespaceKV,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	defer host.Stop(context.Background())
	result, err := maintenance.Clean(ctx, account)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": len(result.Errors) == 0, "namespace": result.Namespace,
		"deleted": result.Deleted, "kept": result.Kept, "errors": result.Errors,
	})
}
