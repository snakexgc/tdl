package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

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
		result.Available = true
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
	writeJSON(w, http.StatusOK, checkAria2(r.Context(), config.Get().Aria2))
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

type aria2TaskRecord struct {
	GID          string    `json:"gid"`
	TaskID       string    `json:"task_id"`
	DownloadURL  string    `json:"download_url"`
	Dir          string    `json:"dir"`
	Out          string    `json:"out"`
	Connections  int       `json:"connections"`
	TransferMode string    `json:"transfer_mode,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Status       string    `json:"status"`
	Total        int64     `json:"total"`
	Completed    int64     `json:"completed"`
	Error        string    `json:"error,omitempty"`
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
	Length          string     `json:"length"`
	CompletedLength string     `json:"completedLength"`
	Path            string     `json:"path"`
	URIs            []aria2URI `json:"uris"`
}

type aria2URI struct {
	URI string `json:"uri"`
}

func (s *Server) startAria2SyncLoop(ctx context.Context) {
	if s.opts.NamespaceKV == nil || s.opts.KVEngine == nil {
		return
	}
	if config.EffectiveDownloaderMode(config.Get()) != config.DownloaderModeAria2 {
		return
	}
	go func() {
		_ = s.syncAria2Statuses(ctx)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.syncAria2Statuses(ctx)
			}
		}
	}()
}

func (s *Server) syncAria2Statuses(ctx context.Context) error {
	if s.opts.KVEngine == nil || s.opts.NamespaceKV == nil {
		return nil
	}
	if config.EffectiveDownloaderMode(config.Get()) != config.DownloaderModeAria2 {
		return nil
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return err
	}
	pairs := meta[s.namespace()]

	records, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return err
	}

	statusByGID, err := fetchAria2Statuses(ctx, config.Get().Aria2)
	if err != nil {
		return err
	}
	s.discoverAria2RecordsFromDownloadLinks(ctx, pairs, records, recordsByTask, statusByGID, config.Get())

	taskCompleted := map[string]bool{}
	taskHasAria2 := map[string]bool{}

	for taskID, taskRecords := range recordsByTask {
		taskHasAria2[taskID] = true
		for _, record := range taskRecords {
			updated := record
			if st, ok := statusByGID[record.GID]; ok {
				updated.Status = normalizedAria2Status(st.Status)
				updated.Total, updated.Completed = aria2Lengths(st)
				updated.Error = strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage)
			}

			data, err := json.Marshal(updated)
			if err != nil {
				continue
			}
			_ = s.opts.NamespaceKV.Set(ctx, aria2TaskKeyPrefix+record.GID, data)

			if updated.Status == aria2StatusComplete && (updated.Total == 0 || updated.Completed >= updated.Total) {
				taskCompleted[taskID] = true
			}
		}
	}

	now := time.Now()

	aria2Index := map[string]time.Time{}
	if idxData, err := s.opts.NamespaceKV.Get(ctx, aria2TaskIndexKey); err == nil {
		_ = json.Unmarshal(idxData, &aria2Index)
	}
	for _, record := range records {
		if record.Status != aria2StatusComplete {
			aria2Index[record.GID] = now
		}
	}
	if idxData, err := json.Marshal(aria2Index); err == nil {
		_ = s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, idxData)
	}

	ttl := httpdl.LinkTTL(config.Get().HTTP)

	downloadIndex := map[string]time.Time{}
	if idxData, err := s.opts.NamespaceKV.Get(ctx, downloadTaskIndexKey); err == nil {
		_ = json.Unmarshal(idxData, &downloadIndex)
	}
	for taskID := range taskHasAria2 {
		if !taskCompleted[taskID] {
			downloadIndex[taskID] = now
			// Slide the link's real expiry clock: a queued/paused/errored task is
			// still "active" for link-lifetime purposes even though aria2 is not
			// fetching it, so refresh the record itself (not just the index) or the
			// link would expire from its original creation time and 404 mid-queue.
			s.refreshDownloadTaskActivity(ctx, taskID, now, ttl)
		}
	}
	if idxData, err := json.Marshal(downloadIndex); err == nil {
		_ = s.opts.NamespaceKV.Set(ctx, downloadTaskIndexKey, idxData)
	}

	for taskID, isCompleted := range taskCompleted {
		if !isCompleted {
			continue
		}
		s.markDownloadTaskDownloaded(ctx, taskID)
	}

	if ttl > 0 && len(downloadIndex) > 0 {
		dlChanged := false
		for taskID, indexedAt := range downloadIndex {
			if !indexedAt.Add(ttl).Before(now) {
				continue
			}
			// Confirm against the record's own activity clock before deleting; the
			// download proxy refreshes records out-of-band, so the index snapshot
			// can lag a still-active link.
			if base, ok := s.downloadTaskActivity(ctx, taskID); ok && !base.Add(ttl).Before(now) {
				downloadIndex[taskID] = base
				dlChanged = true
				continue
			}
			_ = s.opts.NamespaceKV.Delete(ctx, downloadTaskKeyPrefix+taskID)
			delete(downloadIndex, taskID)
			dlChanged = true
		}
		if dlChanged {
			if idxData, err := json.Marshal(downloadIndex); err == nil {
				_ = s.opts.NamespaceKV.Set(ctx, downloadTaskIndexKey, idxData)
			}
		}
	}

	arIndexChanged := false
	for gid, createdAt := range aria2Index {
		if ttl > 0 && createdAt.Add(ttl).Before(now) {
			_ = s.opts.NamespaceKV.Delete(ctx, aria2TaskKeyPrefix+gid)
			delete(aria2Index, gid)
			arIndexChanged = true
			continue
		}
		if record, ok := records[aria2TaskKeyPrefix+gid]; ok && record.TaskID != "" {
			if _, exists := downloadIndex[record.TaskID]; !exists {
				_ = s.opts.NamespaceKV.Delete(ctx, aria2TaskKeyPrefix+gid)
				delete(aria2Index, gid)
				arIndexChanged = true
			}
		}
	}
	if arIndexChanged {
		if idxData, err := json.Marshal(aria2Index); err == nil {
			_ = s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, idxData)
		}
	}

	return nil
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

type downloadLinkTarget struct {
	TaskID      string
	DownloadURL string
	FileName    string
}

func (s *Server) discoverAria2RecordsFromDownloadLinks(ctx context.Context, pairs map[string][]byte, records map[string]aria2TaskRecord, byTask map[string][]aria2TaskRecord, statusByGID map[string]aria2Status, cfg *config.Config) {
	if len(statusByGID) == 0 {
		return
	}
	if cfg == nil {
		cfg = config.Get()
	}

	publicBaseURL := ""
	threads := config.EffectiveThreads(cfg)
	connections := 1
	transferMode := config.HTTPTransferModeSourceParallel
	if cfg != nil {
		publicBaseURL = cfg.HTTP.PublicBaseURL
		connections = config.HTTPRangeConnectionsFor(cfg.HTTP, threads)
		transferMode = config.EffectiveHTTPTransferMode(cfg)
	}
	targetsByID, targetsByURL := downloadLinkTargets(pairs, cfg)
	if len(targetsByID) == 0 {
		return
	}

	changedTasks := map[string]struct{}{}
	for _, status := range statusByGID {
		if status.GID == "" {
			continue
		}
		key := aria2TaskKeyPrefix + status.GID
		if _, exists := records[key]; exists {
			continue
		}
		target, downloadURL, ok := aria2StatusDownloadTarget(status, targetsByID, targetsByURL, publicBaseURL)
		if !ok {
			continue
		}

		dir, out := aria2StatusPathOptions(status)
		if out == "" {
			out = target.FileName
		}
		total, completed := aria2Lengths(status)
		record := aria2TaskRecord{
			GID:          status.GID,
			TaskID:       target.TaskID,
			DownloadURL:  downloadURL,
			Dir:          dir,
			Out:          out,
			Connections:  connections,
			TransferMode: transferMode,
			CreatedAt:    time.Now(),
			Status:       normalizedAria2Status(status.Status),
			Total:        total,
			Completed:    completed,
			Error:        strings.TrimSpace(status.ErrorCode + " " + status.ErrorMessage),
		}

		records[key] = record
		byTask[target.TaskID] = append(byTask[target.TaskID], record)
		changedTasks[target.TaskID] = struct{}{}
		if s.opts.NamespaceKV != nil {
			_ = s.saveAria2Record(ctx, record)
		}
	}

	for taskID := range changedTasks {
		sort.SliceStable(byTask[taskID], func(i, j int) bool {
			return byTask[taskID][i].CreatedAt.Before(byTask[taskID][j].CreatedAt)
		})
	}
}

func downloadLinkTargets(pairs map[string][]byte, cfg *config.Config) (map[string]downloadLinkTarget, map[string]downloadLinkTarget) {
	byID := map[string]downloadLinkTarget{}
	byURL := map[string]downloadLinkTarget{}
	publicBaseURL := ""
	if cfg != nil {
		publicBaseURL = cfg.HTTP.PublicBaseURL
	}

	for key, data := range pairs {
		if !isDownloadTaskRecordKey(key) {
			continue
		}
		var task persistentDownloadTask
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		if task.ID == "" {
			task.ID = strings.TrimPrefix(key, downloadTaskKeyPrefix)
		}
		if task.ID == "" {
			continue
		}
		target := downloadLinkTarget{
			TaskID:      task.ID,
			DownloadURL: downloadURL(publicBaseURL, task.ID),
			FileName:    task.FileName,
		}
		byID[target.TaskID] = target
		byURL[target.DownloadURL] = target
	}
	return byID, byURL
}

func aria2StatusDownloadTarget(status aria2Status, targetsByID, targetsByURL map[string]downloadLinkTarget, publicBaseURL string) (downloadLinkTarget, string, bool) {
	for _, file := range status.Files {
		for _, uri := range file.URIs {
			raw := strings.TrimSpace(uri.URI)
			if raw == "" {
				continue
			}
			if target, ok := targetsByURL[raw]; ok {
				return target, raw, true
			}
			id := downloadTaskIDFromURL(raw, publicBaseURL)
			if id == "" {
				continue
			}
			if target, ok := targetsByID[id]; ok {
				return target, raw, true
			}
		}
	}
	return downloadLinkTarget{}, "", false
}

func downloadTaskIDFromURL(raw, publicBaseURL string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
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
			return ""
		}
	}
	if !strings.HasPrefix(u.Path, expectedPath) {
		return ""
	}

	id := strings.TrimPrefix(u.Path, expectedPath)
	if idx := strings.Index(id, "/"); idx >= 0 {
		id = id[:idx]
	}
	id, err = url.PathUnescape(id)
	if err != nil || id == "" || strings.Contains(id, "/") || !isDownloadTaskRecordKey(downloadTaskKeyPrefix+id) {
		return ""
	}
	return id
}

func aria2StatusPathOptions(status aria2Status) (dir, out string) {
	if len(status.Files) == 0 || status.Files[0].Path == "" {
		return "", ""
	}
	path := filepath.Clean(status.Files[0].Path)
	if path == "." {
		return "", ""
	}
	return filepath.Dir(path), filepath.Base(path)
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
	if err := callAria2(ctx, cfg, "aria2.addUri", []any{[]string{uri}, options}, &gid); err != nil {
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

func (s *Server) saveAria2Record(ctx context.Context, record aria2TaskRecord) error {
	if s.opts.NamespaceKV == nil {
		return errors.New("namespace kv storage is not configured")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task record")
	}
	if err := s.opts.NamespaceKV.Set(ctx, aria2TaskKeyPrefix+record.GID, data); err != nil {
		return errors.Wrap(err, "save aria2 task record")
	}

	index := map[string]time.Time{}
	indexData, err := s.opts.NamespaceKV.Get(ctx, aria2TaskIndexKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return errors.Wrap(err, "load aria2 task index")
	}
	if len(indexData) > 0 {
		if err := json.Unmarshal(indexData, &index); err != nil {
			return errors.Wrap(err, "decode aria2 task index")
		}
	}
	index[record.GID] = record.CreatedAt
	next, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task index")
	}
	if err := s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, next); err != nil {
		return errors.Wrap(err, "save aria2 task index")
	}
	return nil
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
	if cfg.Aria2.Secret != "" || bytes.Contains(body, []byte("/download/")) {
		connections := config.HTTPRangeConnectionsFor(cfg.HTTP, config.EffectiveThreads(cfg))
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
	if method != "aria2.addUri" {
		return
	}
	normalizeAria2AddURIParams(request, publicBaseURL, connections)
}

func normalizeAria2MulticallAddURIRequest(request map[string]any, publicBaseURL string, connections int) {
	method, _ := request["methodName"].(string)
	if method != "aria2.addUri" {
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
