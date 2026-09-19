package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.From(s.opts.Context)
	sessions, sessionsErr := s.listUserSessions(r.Context())
	resp := map[string]any{
		fieldNamespace:  s.namespace(),
		"watch_running": s.watchRunning(),
		"allowed_users": cfg.Bot.AllowedUsers,
		"sessions":      sessions,
	}
	if sessionsErr != nil {
		resp["sessions_error"] = sessionsErr.Error()
	}
	if s.opts.NamespaceKV == nil {
		resp["valid"] = false
		resp["status"] = "namespace kv storage is not configured"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	checkCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	user, err := login.CheckSession(checkCtx, login.SessionOptions{
		Checker:     s.opts.SessionChecker,
		Connections: s.opts.Connections, Credentials: s.opts.Credentials, Account: types.AccountID(s.opts.Namespace),
		KV:               s.opts.NamespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	})
	if err != nil {
		resp["valid"] = false
		resp["status"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp["valid"] = true
	resp["status"] = "authorized"
	resp["user"] = telegramUserInfo(user)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUserSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestReboot == nil {
		writeError(w, http.StatusBadRequest, errors.New("reboot is not available in this mode"))
		return
	}
	var req struct {
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	changed, err := s.accountActions.Switch(r.Context(), types.AccountID(s.namespace()), req.Namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldNamespace: strings.TrimSpace(req.Namespace), "restarting": changed})
	if changed {
		_ = http.NewResponseController(w).Flush()
		s.opts.RequestReboot()
	}
}

type userSessionOption = ports.SessionOption

func (s *Server) listUserSessions(ctx context.Context) ([]userSessionOption, error) {
	return s.sessionCatalog.List(ctx)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	namespace, err := config.NormalizeNamespace(req.Namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if namespace == s.namespace() {
		writeError(w, http.StatusBadRequest, errors.New("当前用户正在运行中，请先切换到其他用户后再删除。"))
		return
	}

	deleted, err := s.deleteUserSession(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		fieldNamespace: namespace,
		fieldDeleted:   deleted,
		fieldMessage:   "用户登录数据已删除。",
	})
}

func (s *Server) deleteUserSession(ctx context.Context, namespace string) (int, error) {
	return s.sessionCatalog.Delete(ctx, namespace)
}

func (s *Server) handleSpamCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	clean, err := s.accountActions.CheckSpam(r.Context(), types.AccountID(s.namespace()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clean": clean})
}

func telegramUserInfo(user *tg.User) map[string]any {
	if user == nil {
		return map[string]any{}
	}
	name := strings.TrimSpace(strings.TrimSpace(user.FirstName + " " + user.LastName))
	return map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"name":       name,
		"phone":      user.Phone,
		"bot":        user.Bot,
		"premium":    user.Premium,
		"restricted": user.Restricted,
		"verified":   user.Verified,
	}
}

func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, s.login.Status())
}

func (s *Server) handleLoginPhoneStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Phone     string `json:"phone"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.StartPhone(r.Context(), req.Phone, req.Namespace); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.Status())
}

func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.SubmitCode(req.Code); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.Status())
}

func (s *Server) handleLoginPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.SubmitPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.Status())
}

func (s *Server) handleLoginCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	s.login.Cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
