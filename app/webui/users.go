package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.Get()
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
	namespace, err := config.NormalizeNamespace(req.Namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg := config.Get()
	if cfg != nil && cfg.Namespace == namespace {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			fieldNamespace: namespace,
			fieldMessage:   "当前已经是该用户。",
		})
		return
	}
	exists, err := s.userSessionExists(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		writeError(w, http.StatusBadRequest, errors.New("请选择已有登录用户，未登录的用户请先在用户登录页面完成登录。"))
		return
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	next, err := config.Clone(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	next.Namespace = namespace
	if err := config.Set(next); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		fieldNamespace: namespace,
		fieldMessage:   "用户已切换，正在重启以加载该用户的数据。",
	})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestReboot()
	}()
}

type userSessionOption struct {
	Namespace string `json:"namespace"`
	Current   bool   `json:"current"`
}

func (s *Server) listUserSessions(ctx context.Context) ([]userSessionOption, error) {
	if s.opts.KVEngine == nil {
		return nil, errors.New("kv engine is not configured")
	}
	namespaces, err := s.opts.KVEngine.Namespaces()
	if err != nil {
		return nil, errors.Wrap(err, "list user session files")
	}
	sort.Strings(namespaces)

	current := s.namespace()
	sessions := make([]userSessionOption, 0, len(namespaces))
	seen := map[string]struct{}{}
	for _, namespace := range namespaces {
		normalized, err := config.NormalizeNamespace(namespace)
		if err != nil || normalized != namespace {
			continue
		}
		if _, ok := seen[namespace]; ok {
			continue
		}
		kvd, err := s.opts.KVEngine.Open(namespace)
		if err != nil {
			continue
		}
		session, err := kvd.Get(ctx, userSessionKey)
		if err != nil || len(session) == 0 {
			continue
		}
		seen[namespace] = struct{}{}
		sessions = append(sessions, userSessionOption{
			Namespace: namespace,
			Current:   namespace == current,
		})
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Current != sessions[j].Current {
			return sessions[i].Current
		}
		return sessions[i].Namespace < sessions[j].Namespace
	})
	return sessions, nil
}

func (s *Server) userSessionExists(ctx context.Context, namespace string) (bool, error) {
	sessions, err := s.listUserSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Namespace == namespace {
			return true, nil
		}
	}
	return false, nil
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
	if s.opts.KVEngine == nil {
		return 0, errors.New("kv engine is not configured")
	}
	exists, err := s.userSessionExists(ctx, namespace)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, errors.New("请选择已有登录用户。")
	}

	kvd, err := s.opts.KVEngine.Open(namespace)
	if err != nil {
		return 0, errors.Wrap(err, "open user storage")
	}

	deleted := 0
	for _, key := range []string{userSessionKey, userAppKey} {
		ok, err := deleteUserKey(ctx, kvd, key)
		if err != nil {
			return deleted, err
		}
		if ok {
			deleted++
		}
	}
	return deleted, nil
}

func deleteUserKey(ctx context.Context, kvd storage.Storage, key string) (bool, error) {
	if _, err := kvd.Get(ctx, key); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return false, nil
		}
		return false, errors.Wrapf(err, "check user key %s", key)
	}
	if err := kvd.Delete(ctx, key); err != nil {
		return false, errors.Wrapf(err, "delete user key %s", key)
	}
	return true, nil
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
	writeJSON(w, http.StatusOK, s.login.status())
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
	if err := s.login.startPhone(r.Context(), req.Phone, req.Namespace); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
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
	if err := s.login.submitCode(req.Code); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
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
	if err := s.login.submitPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
}

func (s *Server) handleLoginCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	s.login.cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
