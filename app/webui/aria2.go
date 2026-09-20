package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	ariacomponent "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/ecual/aria2rpc"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/pkg/config"
)

const aria2AddURIMethod = "aria2.addUri"

type aria2GlobalStat struct {
	DownloadSpeed string `json:"downloadSpeed"`
	NumActive     string `json:"numActive"`
	NumWaiting    string `json:"numWaiting"`
	NumStopped    string `json:"numStopped"`
}

type aria2DashboardStat struct {
	Available        bool
	DownloadSpeedBPS int64
	ActiveTasks      int64
	WaitingTasks     int64
	StoppedTasks     int64
}

func (s aria2DashboardStat) TotalTasks() int64 {
	return s.ActiveTasks + s.WaitingTasks + s.StoppedTasks
}

func fetchAria2DashboardStat(ctx context.Context, cfg config.Aria2Config) (aria2DashboardStat, error) {
	var result aria2DashboardStat
	if strings.TrimSpace(cfg.RPCURL) == "" {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	var stat aria2GlobalStat
	if err := callAria2(ctx, cfg, "aria2.getGlobalStat", []any{}, &stat); err != nil {
		return result, err
	}
	result.Available = true
	result.DownloadSpeedBPS = parseAria2Length(stat.DownloadSpeed)
	result.ActiveTasks = parseAria2Length(stat.NumActive)
	result.WaitingTasks = parseAria2Length(stat.NumWaiting)
	result.StoppedTasks = parseAria2Length(stat.NumStopped)
	return result, nil
}

type aria2CheckResult struct {
	OK         bool   `json:"ok"`
	Configured bool   `json:"configured"`
	RPCURL     string `json:"rpc_url"`
	Version    string `json:"version,omitempty"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) handleAria2Check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, checkAria2(r.Context(), config.From(s.opts.Context).Aria2))
}

func checkAria2(ctx context.Context, cfg config.Aria2Config) aria2CheckResult {
	rpcURL := strings.TrimSpace(cfg.RPCURL)
	result := aria2CheckResult{
		RPCURL: rpcURL,
	}
	if rpcURL == "" {
		result.Message = "尚未配置 aria2.rpc_url。请先在配置设置中填写 aria2 JSON-RPC 地址。"
		return result
	}
	result.Configured = true

	parsed, err := url.Parse(rpcURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		result.Message = "aria2.rpc_url 格式不正确。"
		if err != nil {
			result.Error = err.Error()
		}
		return result
	}

	var version struct {
		Version string `json:"version"`
	}
	if err := callAria2(ctx, cfg, "aria2.getVersion", []any{}, &version); err != nil {
		result.Message = "无法连接 aria2 JSON-RPC。请检查地址、端口、网络和密钥。"
		result.Error = err.Error()
		return result
	}

	result.OK = true
	result.Version = version.Version
	if version.Version != "" {
		result.Message = "aria2 连接正常，版本：" + version.Version
	} else {
		result.Message = "aria2 连接正常。"
	}
	return result
}

type aria2TaskRecord = types.Aria2TaskRecord

func (s *Server) aria2Observer() ariacomponent.Observer {
	cfg := config.From(s.opts.Context)
	return ariacomponent.Observer{Logger: logctx.From(s.opts.Context).With(zap.String("account", s.namespace())), Client: aria2rpc.NewClient(cfg.Aria2), Repository: taskhub.Aria2Observations{Links: taskhub.LinkRepository{Store: s.opts.NamespaceKV, Engine: s.opts.KVEngine, Namespace: s.namespace()}}, PublicBaseURL: cfg.HTTP.PublicBaseURL, TTL: time.Duration(cfg.HTTP.DownloadLinkTTLHours) * time.Hour}
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
		if record.Deleted {
			continue
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

func addAria2URI(ctx context.Context, cfg config.Aria2Config, uri, out string, connections int) (string, error) {
	options := map[string]any{
		"continue":                  valueTrue,
		"allow-piece-length-change": valueTrue,
		"allow-overwrite":           valueTrue,
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-webui-aria2",
	}
	applyTDLAria2HTTPConnectionOptions(options, connections)
	if cfg.Dir != "" {
		options["dir"] = cfg.Dir
	}
	if out != "" {
		options["out"] = out
	}
	var gid string
	if err := callAria2(ctx, cfg, aria2AddURIMethod, []any{[]string{uri}, options}, &gid); err != nil {
		return "", err
	}
	if gid == "" {
		return "", errors.New("aria2 returned empty gid")
	}
	return gid, nil
}

func applyTDLAria2HTTPConnectionOptions(options map[string]any, connections int) {
	if connections < 1 {
		connections = 1
	}
	value := strconv.Itoa(connections)
	options["split"] = value
	options["max-connection-per-server"] = value
	options["min-split-size"] = tdlAria2PieceSize
	options["piece-length"] = tdlAria2PieceSize
	options["timeout"] = tdlAria2TimeoutSeconds
}

func configureAria2MaxConcurrentDownloads(ctx context.Context, cfg config.Aria2Config, limit int) error {
	if limit < 1 {
		return errors.New("limit must be greater than 0")
	}
	var result string
	if err := callAria2(ctx, cfg, "aria2.changeGlobalOption", []any{
		map[string]any{
			"max-concurrent-downloads": strconv.Itoa(limit),
		},
	}, &result); err != nil {
		return err
	}
	if result != "OK" {
		return fmt.Errorf("unexpected aria2 response %q", result)
	}
	return nil
}

func (s *Server) handleAria2Proxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	cfg := config.From(s.opts.Context)
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "read request"))
		return
	}
	if cfg.Aria2.Secret != "" || bytes.Contains(body, []byte("/download/")) {
		connections := config.EffectivePoolSize(cfg)
		body, err = rewriteAria2ProxyRequest(body, cfg.HTTP.PublicBaseURL, cfg.Aria2.Secret, connections)
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
	resp, err := aria2rpc.Forward(r.Context(), client, cfg.Aria2.RPCURL, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.Wrap(err, "forward aria2 request"))
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func rewriteAria2ProxyRequest(body []byte, publicBaseURL, secret string, connections int) ([]byte, error) {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, errors.Wrap(err, "decode aria2 request")
	}
	token := ""
	if secret != "" {
		token = "token:" + secret
	}
	switch value := payload.(type) {
	case map[string]any:
		rewriteAria2RequestObject(value, publicBaseURL, token, connections)
	case []any:
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				rewriteAria2RequestObject(obj, publicBaseURL, token, connections)
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

func rewriteAria2RequestObject(request map[string]any, publicBaseURL, token string, connections int) {
	normalizeAria2AddURIRequest(request, publicBaseURL, connections)
	if token != "" {
		addAria2Token(request, token)
	}
}

func addAria2Token(request map[string]any, token string) {
	method, _ := request["method"].(string)
	if method == "system.multicall" {
		addAria2MulticallToken(request, token)
		return
	}
	if strings.HasPrefix(method, "system.") {
		return
	}
	prependAria2TokenParam(request, token)
}

func addAria2MulticallToken(request map[string]any, token string) {
	params, _ := request["params"].([]any)
	if len(params) == 0 {
		return
	}
	if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
		params = params[1:]
	}
	if len(params) == 0 {
		request["params"] = []any{}
		return
	}
	calls, ok := params[0].([]any)
	if !ok {
		request["params"] = params
		return
	}
	for _, call := range calls {
		obj, ok := call.(map[string]any)
		if !ok {
			continue
		}
		methodName, _ := obj["methodName"].(string)
		if strings.HasPrefix(methodName, "system.") {
			continue
		}
		prependAria2TokenParam(obj, token)
	}
	request["params"] = append([]any{calls}, params[1:]...)
}

func prependAria2TokenParam(request map[string]any, token string) {
	params, _ := request["params"].([]any)
	if len(params) > 0 {
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			return
		}
	}
	request["params"] = append([]any{token}, params...)
}

func normalizeAria2AddURIRequest(request map[string]any, publicBaseURL string, connections int) {
	method, _ := request["method"].(string)
	if method == "system.multicall" {
		params, _ := request["params"].([]any)
		if len(params) == 0 {
			return
		}
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			params = params[1:]
		}
		if len(params) == 0 {
			return
		}
		calls, ok := params[0].([]any)
		if !ok {
			return
		}
		for _, call := range calls {
			if obj, ok := call.(map[string]any); ok {
				normalizeAria2MulticallAddURIRequest(obj, publicBaseURL, connections)
			}
		}
		return
	}
	if method != aria2AddURIMethod {
		return
	}
	normalizeAria2AddURIParams(request, publicBaseURL, connections)
}

func normalizeAria2MulticallAddURIRequest(request map[string]any, publicBaseURL string, connections int) {
	method, _ := request["methodName"].(string)
	if method != aria2AddURIMethod {
		return
	}
	normalizeAria2AddURIParams(request, publicBaseURL, connections)
}

func normalizeAria2AddURIParams(request map[string]any, publicBaseURL string, connections int) {
	params, _ := request["params"].([]any)
	paramStart := 0
	if len(params) > 0 {
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			paramStart = 1
		}
	}
	if len(params) <= paramStart {
		return
	}
	urls, ok := params[paramStart].([]any)
	if !ok || !hasTDLDownloadURI(urls, publicBaseURL) {
		return
	}

	optionIndex := paramStart + 1
	var options map[string]any
	if len(params) > optionIndex {
		options, _ = params[optionIndex].(map[string]any)
	}
	if options == nil {
		options = map[string]any{}
		if len(params) > optionIndex {
			params[optionIndex] = options
		} else {
			params = append(params, options)
		}
	}
	applyTDLAria2HTTPConnectionOptions(options, connections)
	request["params"] = params
}

func hasTDLDownloadURI(urls []any, publicBaseURL string) bool {
	for _, raw := range urls {
		value, ok := raw.(string)
		if ok && isTDLDownloadURI(value, publicBaseURL) {
			return true
		}
	}
	return false
}

func isTDLDownloadURI(raw, publicBaseURL string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}

	expectedPath := "/download/"
	var base *url.URL
	if publicBaseURL != "" {
		if parsed, err := url.Parse(publicBaseURL); err == nil {
			base = parsed
			expectedPath = strings.TrimRight(parsed.Path, "/") + "/download/"
		}
	}

	if base != nil && base.Host != "" && u.Host != "" {
		if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
			return false
		}
	}
	return strings.HasPrefix(u.Path, expectedPath)
}

func callAria2(ctx context.Context, cfg config.Aria2Config, method string, params []any, result any) error {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	raw, err := aria2rpc.Call(ctx, &http.Client{Timeout: timeout}, cfg.RPCURL, cfg.Secret, method, params, 1)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func parseAria2Length(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}
