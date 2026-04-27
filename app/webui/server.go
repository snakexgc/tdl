package webui

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

//go:embed index.html aria2ng.html static/*
var assets embed.FS

const (
	downloadTaskKeyPrefix = "watch.download."
	aria2TaskKeyPrefix    = "watch.aria2.task."
	aria2TaskIndexKey     = "watch.aria2.index"
)

type Options struct {
	KVEngine        kv.Storage
	Namespace       string
	NamespaceKV     storage.Storage
	AfterConfigSave func(*config.Config)
	RequestReboot   func()
	WatchRunning    func() bool
}

type Server struct {
	opts Options
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Get()
	if cfg == nil || strings.TrimSpace(cfg.WebUI.Listen) == "" {
		return nil
	}
	if strings.TrimSpace(cfg.WebUI.Username) == "" || cfg.WebUI.Password == "" {
		return nil
	}

	server := NewServer(opts)
	httpServer := &http.Server{
		Addr:    cfg.WebUI.Listen,
		Handler: server.routes(),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	return httpServer.ListenAndServe()
}

func NewServer(opts Options) *Server {
	return &Server{opts: opts}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(assets, "static")
	mux.Handle("/static/", s.auth(http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))))
	mux.HandleFunc("/aria2ng.html", s.authFunc(s.handleAsset("aria2ng.html", "text/html; charset=utf-8")))
	mux.HandleFunc("/aria2/jsonrpc", s.authFunc(s.handleAria2Proxy))
	mux.HandleFunc("/api/status", s.authFunc(s.handleStatus))
	mux.HandleFunc("/api/kv/links", s.authFunc(s.handleKVLinks))
	mux.HandleFunc("/api/kv/links/", s.authFunc(s.handleKVLink))
	mux.HandleFunc("/api/user", s.authFunc(s.handleUser))
	mux.HandleFunc("/api/config", s.authFunc(s.handleConfig))
	mux.HandleFunc("/api/system/reboot", s.authFunc(s.handleReboot))
	mux.HandleFunc("/", s.authFunc(s.handleAsset("index.html", "text/html; charset=utf-8")))

	return mux
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := config.Get()
		if cfg == nil || !basicAuthOK(r, cfg.WebUI.Username, cfg.WebUI.Password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="tdl webui"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authFunc(fn http.HandlerFunc) http.HandlerFunc {
	return s.auth(fn).ServeHTTP
}

func basicAuthOK(r *http.Request, username, password string) bool {
	if username == "" || password == "" {
		return false
	}
	gotUser, gotPassword, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPassword), []byte(password)) == 1
	return userOK && passOK
}

func (s *Server) handleAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if name == "index.html" && r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace":     s.namespace(),
		"watch_running": s.watchRunning(),
		"webui": map[string]any{
			"listen": cfg.WebUI.Listen,
			"user":   cfg.WebUI.Username,
		},
		"aria2": map[string]any{
			"rpc_url": cfg.Aria2.RPCURL,
			"proxy":   "/aria2/jsonrpc",
		},
		"http": map[string]any{
			"public_base_url": cfg.HTTP.PublicBaseURL,
			"download_ttl":    cfg.HTTP.DownloadLinkTTLHours,
		},
	})
}

func (s *Server) handleKVLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, statusErr, err := s.listDownloadLinks(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        items,
			"status_error": statusErr,
		})
	default:
		methodNotAllowed(w, "GET")
	}
}

func (s *Server) handleKVLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	if s.opts.NamespaceKV == nil {
		writeError(w, http.StatusInternalServerError, errors.New("namespace kv storage is not configured"))
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/kv/links/"))
	if err != nil || id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	deleted := 0
	if err := s.opts.NamespaceKV.Delete(r.Context(), downloadTaskKeyPrefix+id); err != nil {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "delete download task"))
		return
	}
	deleted++

	records, _, err := s.loadAria2Records()
	if err == nil {
		for key, record := range records {
			if record.TaskID != id {
				continue
			}
			if err := s.opts.NamespaceKV.Delete(r.Context(), key); err != nil {
				writeError(w, http.StatusInternalServerError, errors.Wrap(err, "delete aria2 task record"))
				return
			}
			deleted++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

type downloadLinkItem struct {
	ID         string           `json:"id"`
	Key        string           `json:"key"`
	URL        string           `json:"url"`
	FileName   string           `json:"file_name"`
	FileSize   int64            `json:"file_size"`
	PeerID     int64            `json:"peer_id"`
	MessageID  int              `json:"message_id"`
	CreatedAt  time.Time        `json:"created_at"`
	ExpiresAt  *time.Time       `json:"expires_at,omitempty"`
	Permanent  bool             `json:"permanent"`
	Expired    bool             `json:"expired"`
	Downloaded bool             `json:"downloaded"`
	Status     string           `json:"status"`
	Aria2      []aria2LinkEntry `json:"aria2"`
}

type aria2LinkEntry struct {
	GID         string    `json:"gid"`
	Status      string    `json:"status"`
	Downloaded  bool      `json:"downloaded"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	CreatedAt   time.Time `json:"created_at"`
	Total       int64     `json:"total"`
	Completed   int64     `json:"completed"`
	Error       string    `json:"error,omitempty"`
}

type persistentDownloadTask struct {
	ID        string    `json:"id"`
	PeerID    int64     `json:"peer_id"`
	MessageID int       `json:"message_id"`
	FileName  string    `json:"file_name"`
	FileSize  int64     `json:"file_size"`
	CreatedAt time.Time `json:"created_at"`
}

type aria2TaskRecord struct {
	GID         string    `json:"gid"`
	TaskID      string    `json:"task_id"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	Connections int       `json:"connections"`
	CreatedAt   time.Time `json:"created_at"`
}

type aria2Status struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"`
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	ErrorCode       string      `json:"errorCode"`
	ErrorMessage    string      `json:"errorMessage"`
	Files           []aria2File `json:"files"`
}

type aria2File struct {
	Length          string `json:"length"`
	CompletedLength string `json:"completedLength"`
}

func (s *Server) listDownloadLinks(ctx context.Context) ([]downloadLinkItem, string, error) {
	if s.opts.KVEngine == nil {
		return nil, "", errors.New("kv engine is not configured")
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return nil, "", errors.Wrap(err, "list kv keys")
	}
	pairs := meta[s.namespace()]

	records, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return nil, "", err
	}
	_ = records

	statusByGID, statusErr := fetchAria2Statuses(ctx, config.Get().Aria2)
	var statusErrText string
	if statusErr != nil {
		statusErrText = statusErr.Error()
	}

	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		if strings.HasPrefix(key, downloadTaskKeyPrefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	cfg := config.Get()
	now := time.Now()
	items := make([]downloadLinkItem, 0, len(keys))
	for _, key := range keys {
		var task persistentDownloadTask
		if err := json.Unmarshal(pairs[key], &task); err != nil {
			continue
		}
		if task.ID == "" {
			task.ID = strings.TrimPrefix(key, downloadTaskKeyPrefix)
		}

		item := downloadLinkItem{
			ID:        task.ID,
			Key:       key,
			URL:       downloadURL(cfg.HTTP.PublicBaseURL, task.ID),
			FileName:  task.FileName,
			FileSize:  task.FileSize,
			PeerID:    task.PeerID,
			MessageID: task.MessageID,
			CreatedAt: task.CreatedAt,
			Status:    "not_submitted",
		}
		if cfg.HTTP.DownloadLinkTTLHours <= 0 {
			item.Permanent = true
		} else if !task.CreatedAt.IsZero() {
			expiresAt := task.CreatedAt.Add(time.Duration(cfg.HTTP.DownloadLinkTTLHours) * time.Hour)
			item.ExpiresAt = &expiresAt
			item.Expired = !expiresAt.After(now)
		}

		for _, record := range recordsByTask[task.ID] {
			entry := aria2LinkEntry{
				GID:         record.GID,
				Status:      "registered",
				DownloadURL: record.DownloadURL,
				Dir:         record.Dir,
				Out:         record.Out,
				CreatedAt:   record.CreatedAt,
			}
			if st, ok := statusByGID[record.GID]; ok {
				entry.Status = normalizedAria2Status(st.Status)
				entry.Total, entry.Completed = aria2Lengths(st)
				entry.Downloaded = entry.Status == "complete" && (entry.Total == 0 || entry.Completed >= entry.Total)
				entry.Error = strings.TrimSpace(strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage))
			}
			if entry.Downloaded {
				item.Downloaded = true
			}
			item.Status = entry.Status
			item.Aria2 = append(item.Aria2, entry)
		}
		items = append(items, item)
	}

	return items, statusErrText, nil
}

func (s *Server) loadAria2Records() (map[string]aria2TaskRecord, map[string][]aria2TaskRecord, error) {
	if s.opts.KVEngine == nil {
		return nil, nil, errors.New("kv engine is not configured")
	}
	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return nil, nil, errors.Wrap(err, "list kv keys")
	}
	return s.parseAria2Records(meta[s.namespace()])
}

func (s *Server) parseAria2Records(pairs map[string][]byte) (map[string]aria2TaskRecord, map[string][]aria2TaskRecord, error) {
	records := map[string]aria2TaskRecord{}
	byTask := map[string][]aria2TaskRecord{}
	for key, data := range pairs {
		if !strings.HasPrefix(key, aria2TaskKeyPrefix) || key == aria2TaskIndexKey {
			continue
		}
		var record aria2TaskRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, nil, errors.Wrapf(err, "decode %s", key)
		}
		if record.GID == "" {
			record.GID = strings.TrimPrefix(key, aria2TaskKeyPrefix)
		}
		records[key] = record
		if record.TaskID != "" {
			byTask[record.TaskID] = append(byTask[record.TaskID], record)
		}
	}
	for taskID := range byTask {
		sort.SliceStable(byTask[taskID], func(i, j int) bool {
			return byTask[taskID][i].CreatedAt.Before(byTask[taskID][j].CreatedAt)
		})
	}
	return records, byTask, nil
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.Get()
	resp := map[string]any{
		"namespace":     s.namespace(),
		"watch_running": s.watchRunning(),
		"allowed_users": cfg.Bot.AllowedUsers,
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
		Proxy:            cfg.Proxy,
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

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"config": publicConfig(config.Get())})
	case http.MethodPatch:
		var req struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
			return
		}
		next, err := cloneConfig(config.Get())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for path, raw := range req.Values {
			if isBlankSensitivePatch(path, raw) {
				continue
			}
			if err := setConfigJSONValue(next, path, raw); err != nil {
				writeError(w, http.StatusBadRequest, errors.Wrapf(err, "set %s", path))
				return
			}
		}
		if err := config.Set(next); err != nil {
			writeError(w, http.StatusInternalServerError, errors.Wrap(err, "save config"))
			return
		}
		if s.opts.AfterConfigSave != nil {
			s.opts.AfterConfigSave(next)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"config":  publicConfig(next),
			"message": "配置已保存。大部分运行参数已应用；监听地址、存储、命名空间、bot token 等需要重启后完整生效。",
		})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "正在重启 tdl"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestReboot()
	}()
}

func publicConfig(cfg *config.Config) *config.Config {
	next, err := cloneConfig(cfg)
	if err != nil {
		return config.DefaultConfig()
	}
	next.Bot.Token = ""
	next.Aria2.Secret = ""
	next.WebUI.Password = ""
	return next
}

func cloneConfig(cfg *config.Config) (*config.Config, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var next config.Config
	if err := json.Unmarshal(data, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

func isBlankSensitivePatch(path string, raw json.RawMessage) bool {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "bot.token", "aria2.secret", "webui.password":
	default:
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == ""
}

func setConfigJSONValue(cfg *config.Config, path string, raw json.RawMessage) error {
	return setPathJSONValue(reflect.ValueOf(cfg).Elem(), splitConfigPath(path), raw)
}

func splitConfigPath(path string) []string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setPathJSONValue(value reflect.Value, path []string, raw json.RawMessage) error {
	value = indirectValue(value)
	if len(path) == 0 {
		return errors.New("empty config path")
	}

	switch value.Kind() {
	case reflect.Struct:
		field, ok := fieldByJSONName(value, path[0])
		if !ok {
			return fmt.Errorf("unknown config key %q", path[0])
		}
		if len(path) == 1 {
			return setReflectJSONValue(field, raw)
		}
		return setPathJSONValue(field, path[1:], raw)
	case reflect.Map:
		if len(path) != 1 {
			return fmt.Errorf("config key %q is not an object", path[0])
		}
		key, err := mapKeyValue(value.Type().Key(), path[0])
		if err != nil {
			return err
		}
		item := reflect.New(value.Type().Elem())
		if err := json.Unmarshal(raw, item.Interface()); err != nil {
			return err
		}
		if value.IsNil() {
			value.Set(reflect.MakeMap(value.Type()))
		}
		value.SetMapIndex(key, item.Elem())
		return nil
	default:
		return fmt.Errorf("config key %q cannot be expanded", path[0])
	}
}

func setReflectJSONValue(value reflect.Value, raw json.RawMessage) error {
	if !value.CanSet() {
		return errors.New("config value cannot be set")
	}
	target := reflect.New(value.Type())
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return err
	}
	value.Set(target.Elem())
	return nil
}

func fieldByJSONName(value reflect.Value, name string) (reflect.Value, bool) {
	value = indirectValue(value)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}
		if strings.EqualFold(jsonName, name) || strings.EqualFold(field.Name, name) {
			return value.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func indirectValue(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func mapKeyValue(typ reflect.Type, raw string) (reflect.Value, error) {
	switch typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(raw).Convert(typ), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported map key type %s", typ)
	}
}

func (s *Server) handleAria2Proxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	cfg := config.Get()
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "read request"))
		return
	}
	if cfg.Aria2.Secret != "" {
		body, err = injectAria2Secret(body, cfg.Aria2.Secret)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	timeout := time.Duration(cfg.Aria2.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.Aria2.RPCURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "create aria2 request"))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.Wrap(err, "forward aria2 request"))
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func injectAria2Secret(body []byte, secret string) ([]byte, error) {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, errors.Wrap(err, "decode aria2 request")
	}
	token := "token:" + secret
	switch value := payload.(type) {
	case map[string]any:
		addAria2Token(value, token)
	case []any:
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				addAria2Token(obj, token)
			}
		}
	default:
		return nil, errors.New("aria2 request must be an object or array")
	}
	next, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "encode aria2 request")
	}
	return next, nil
}

func addAria2Token(request map[string]any, token string) {
	params, _ := request["params"].([]any)
	if len(params) > 0 {
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			return
		}
	}
	request["params"] = append([]any{token}, params...)
}

func fetchAria2Statuses(ctx context.Context, cfg config.Aria2Config) (map[string]aria2Status, error) {
	out := map[string]aria2Status{}
	if cfg.RPCURL == "" {
		return out, nil
	}
	for _, call := range []struct {
		method string
		params []any
	}{
		{method: "aria2.tellActive", params: []any{aria2StatusKeys()}},
		{method: "aria2.tellWaiting", params: []any{0, 1000, aria2StatusKeys()}},
		{method: "aria2.tellStopped", params: []any{0, 1000, aria2StatusKeys()}},
	} {
		var statuses []aria2Status
		if err := callAria2(ctx, cfg, call.method, call.params, &statuses); err != nil {
			return out, err
		}
		for _, status := range statuses {
			if status.GID != "" {
				out[status.GID] = status
			}
		}
	}
	return out, nil
}

func aria2StatusKeys() []string {
	return []string{"gid", "status", "totalLength", "completedLength", "errorCode", "errorMessage", "files"}
}

func callAria2(ctx context.Context, cfg config.Aria2Config, method string, params []any, result any) error {
	if cfg.Secret != "" {
		params = append([]any{"token:" + cfg.Secret}, params...)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "tdl-webui",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RPCURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("aria2 rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return json.Unmarshal(decoded.Result, result)
}

func aria2Lengths(status aria2Status) (total, completed int64) {
	total = parseAria2Length(status.TotalLength)
	completed = parseAria2Length(status.CompletedLength)
	if total == 0 && len(status.Files) > 0 {
		for _, file := range status.Files {
			total += parseAria2Length(file.Length)
			completed += parseAria2Length(file.CompletedLength)
		}
	}
	return total, completed
}

func parseAria2Length(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func normalizedAria2Status(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func downloadURL(baseURL, taskID string) string {
	if baseURL == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/download/" + url.PathEscape(taskID)
	return u.String()
}

func (s *Server) namespace() string {
	if s.opts.Namespace != "" {
		return s.opts.Namespace
	}
	cfg := config.Get()
	if cfg != nil {
		return cfg.Namespace
	}
	return "default"
}

func (s *Server) watchRunning() bool {
	if s.opts.WatchRunning == nil {
		return false
	}
	return s.opts.WatchRunning()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
