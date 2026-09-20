package webui

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w, "GET, POST")
		return
	}
	if s.opts.ResetPlan == nil || s.opts.RequestReset == nil {
		writeError(w, http.StatusBadRequest, errors.New("当前运行模式不支持完全重置。"))
		return
	}
	targets, err := s.opts.ResetPlan.Targets()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, errors.New("reset requires application/json"))
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if decoder.Decode(new(any)) != io.EOF || request.Confirmation != "RESET_TDL" {
		writeError(w, http.StatusBadRequest, errors.New("请先确认完全重置警告。"))
		return
	}
	if !s.shutdownRequested.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, errors.New(shutdownInProgressMessage))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, fieldMessage: "重置请求已接受，TDL 正在停止服务并清理运行数据。完成后程序退出，请重新启动、配置并登录。清理结果请查看程序控制台。"})
	_ = http.NewResponseController(w).Flush()
	s.opts.RequestReset()
}
