package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/pkg/config"
)

type loginFailure struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.sessionOK(r) {
			if wantsHTML(r) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authFunc(fn http.HandlerFunc) http.HandlerFunc {
	return s.auth(fn).ServeHTTP
}

func credentialsOK(gotUser, gotPassword, username, password string) bool {
	if username == "" || password == "" || gotUser == "" || gotPassword == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPassword), []byte(password)) == 1
	return userOK && passOK
}

func (s *Server) sessionOK(r *http.Request) bool {
	cookie, err := r.Cookie(webUICookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	token := cookie.Value
	now := time.Now()

	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.pruneSessionsLocked(now)
	expiresAt, ok := s.sessions[token]
	if !ok || !expiresAt.After(now) {
		delete(s.sessions, token)
		return false
	}
	s.sessions[token] = now.Add(webUISessionTTL)
	return true
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	s.sessionMu.Lock()
	s.pruneSessionsLocked(time.Now())
	s.sessions[token] = time.Now().Add(webUISessionTTL)
	s.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     webUICookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(webUISessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(webUICookieName); err == nil {
		s.sessionMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     webUICookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}

func newSessionToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", errors.Wrap(err, "generate session token")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func wantsHTML(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/views/") || strings.HasPrefix(r.URL.Path, "/aria2/") {
		return false
	}
	accept := r.Header.Get("Accept")
	return r.Method == http.MethodGet && (accept == "" || strings.Contains(accept, "text/html"))
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionOK(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.serveAsset(w, r, "login.html", "text/html; charset=utf-8")
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if !s.sessionOK(r) {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	cfg := config.Get()
	user := ""
	if cfg != nil {
		user = cfg.WebUI.Username
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
		"webui": map[string]any{
			fieldUsingDefaultCredentials: config.UsesDefaultWebUICredentials(cfg),
		},
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if retryAfter, blocked := s.loginBlocked(r, time.Now()); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, errors.New("too many failed login attempts; try again later"))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	cfg := config.Get()
	if cfg == nil || !credentialsOK(strings.TrimSpace(req.Username), req.Password, cfg.WebUI.Username, cfg.WebUI.Password) {
		s.recordLoginFailure(r, time.Now())
		writeError(w, http.StatusUnauthorized, errors.New("用户名或密码错误"))
		return
	}
	s.clearLoginFailures(r)
	if err := s.issueSession(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                         true,
		fieldUsingDefaultCredentials: config.UsesDefaultWebUICredentials(cfg),
	})
}

func (s *Server) pruneSessionsLocked(now time.Time) {
	for token, expiresAt := range s.sessions {
		if !expiresAt.After(now) {
			delete(s.sessions, token)
		}
	}
}

func (s *Server) loginBlocked(r *http.Request, now time.Time) (time.Duration, bool) {
	key := loginClientKey(r)
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.pruneLoginFailuresLocked(now)
	failure, ok := s.logins[key]
	if !ok || !failure.blockedUntil.After(now) {
		return 0, false
	}
	return failure.blockedUntil.Sub(now), true
}

func (s *Server) recordLoginFailure(r *http.Request, now time.Time) {
	key := loginClientKey(r)
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.pruneLoginFailuresLocked(now)
	failure := s.logins[key]
	if failure.windowStart.IsZero() || now.Sub(failure.windowStart) >= webUILoginFailureWindow {
		failure = loginFailure{windowStart: now}
	}
	failure.count++
	if failure.count >= webUIMaxLoginFailures {
		failure.blockedUntil = now.Add(webUILoginLockout)
	}
	s.logins[key] = failure
}

func (s *Server) clearLoginFailures(r *http.Request) {
	s.loginMu.Lock()
	delete(s.logins, loginClientKey(r))
	s.loginMu.Unlock()
}

func (s *Server) pruneLoginFailuresLocked(now time.Time) {
	for key, failure := range s.logins {
		if failure.blockedUntil.After(now) || now.Sub(failure.windowStart) < webUILoginFailureWindow {
			continue
		}
		delete(s.logins, key)
	}
}

func loginClientKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if value := strings.TrimSpace(r.RemoteAddr); value != "" {
		return value
	}
	return "unknown"
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	s.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
