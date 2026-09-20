package webui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	logAccountComponent = "account.telegram"
	logLocalComponent   = "downloader.local"
	logForwardComponent = "forwarder"
	logSystemComponent  = "system"
	logDiagnosticKind   = "diagnostic"
	logRuntimeKind      = "runtime"
	logInfoLevel        = "info"
	logHTTPAdapter      = "http"
	logAria2Adapter     = "aria2"
	logBotAdapter       = "bot"
	logBotComponent     = "console.bot"
	logAria2Component   = "downloader.aria2"
	logHTTPComponent    = "proxy.range"
)

type logComponent struct {
	ID      string           `json:"id"`
	Title   string           `json:"title"`
	Feature manifest.Feature `json:"feature"`
}
type logItem struct {
	logging.Entry
	Feature        string `json:"feature"`
	ComponentTitle string `json:"component_title"`
}

// Process and transport logger names are normalized at the composition boundary.
func logComponentID(id, caller, logger string) string {
	aliases := map[string]string{"host.bot": logBotComponent, "host.panel": "panel.webui", "host.aria2": logAria2Component, logHTTPAdapter: logHTTPComponent, "watch": logAccountComponent, logBotAdapter: logBotComponent, logAria2Adapter: logAria2Component}
	if mapped := aliases[id]; mapped != "" {
		return mapped
	}
	if id != "" && id != logSystemComponent {
		return id
	}
	if strings.Contains(logger, "local-downloader") {
		return logLocalComponent
	}
	if strings.Contains(logger, logAria2Adapter) {
		return logAria2Component
	}
	if strings.Contains(logger, "http-download") {
		return logHTTPComponent
	}
	if logger == "td" || strings.HasPrefix(logger, "td.") || logger == "updates" || strings.HasPrefix(logger, "updates.") {
		return logAccountComponent
	}
	directory, _, _ := strings.Cut(caller, "/")
	switch directory {
	case "watch":
		if strings.Contains(caller, "reaction") {
			return "trigger.reaction"
		}
		if strings.Contains(caller, "forward") {
			return "trigger.forward"
		}
		if strings.Contains(caller, "submission") {
			return "trigger.download"
		}
		return logAccountComponent
	case "tclient", "dcpool", "login":
		return logAccountComponent
	case logBotAdapter:
		return logBotComponent
	case "updater":
		return "update.self"
	case logHTTPAdapter:
		return logHTTPComponent
	case "downloader":
		return logLocalComponent
	}
	if strings.Contains(directory, ".") || directory == logForwardComponent {
		return directory
	}
	return logSystemComponent
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	store := logging.From(s.opts.Context)
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("日志服务未初始化"))
		return
	}
	query := r.URL.Query()
	before, err := strconv.ParseUint(defaultString(query.Get("before"), "0"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("无效的日志游标"))
		return
	}
	limit, err := strconv.Atoi(defaultString(query.Get("limit"), "200"))
	if err != nil || limit < 1 || limit > 500 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("日志条数必须介于 1 和 500"))
		return
	}
	level := query.Get("level")
	if level != "" && level != logDebugLevel && level != logInfoLevel && level != "warn" && level != fieldError {
		writeError(w, http.StatusBadRequest, fmt.Errorf("无效的日志级别"))
		return
	}
	kind := query.Get("kind")
	if kind != "" && kind != logRuntimeKind && kind != logDiagnosticKind {
		writeError(w, http.StatusBadRequest, fmt.Errorf("无效的日志类型"))
		return
	}
	var since time.Time
	if raw := query.Get("since"); raw != "" {
		since, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("无效的开始时间"))
			return
		}
	}
	search := strings.ToLower(strings.TrimSpace(query.Get("q")))
	if utf8.RuneCountInString(search) > 256 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("搜索内容过长"))
		return
	}
	components := []logComponent{}
	byID := map[string]logComponent{}
	for _, definition := range s.opts.Catalog.Definitions() {
		m := definition.Manifest
		feature := m.Feature
		if feature.ID == "" {
			feature = manifest.Feature{ID: "extensions", Title: "扩展功能", Order: 1000}
		}
		item := logComponent{ID: m.ID, Title: m.Title, Feature: feature}
		components = append(components, item)
		byID[item.ID] = item
	}
	system := logComponent{ID: logSystemComponent, Title: "系统运行", Feature: manifest.Feature{ID: logSystemComponent, Title: "系统运行", Order: 1100}}
	components = append(components, system)
	byID[system.ID] = system
	snapshot, warning := store.Snapshot()
	items := []logItem{}
	total := 0
	more := false
	retained := 0
	export := query.Get("download") == "1"
	for _, entry := range snapshot {
		if entry.Account != "" && entry.Account != s.namespace() {
			continue
		}
		retained++
		entry.Component = logComponentID(entry.Component, entry.Caller, entry.Logger)
		component, ok := byID[entry.Component]
		if !ok {
			component = logComponent{ID: entry.Component, Title: entry.Component, Feature: system.Feature}
			byID[component.ID] = component
			components = append(components, component)
		}
		if query.Get("component") != "" && query.Get("component") != entry.Component {
			continue
		}
		if query.Get("feature") != "" && query.Get("feature") != component.Feature.ID {
			continue
		}
		entryLevel := entry.Level
		if entryLevel == "dpanic" || entryLevel == "panic" || entryLevel == "fatal" {
			entryLevel = fieldError
		}
		if level != "" && entryLevel != level {
			continue
		}
		if kind != "" && entry.Kind != kind || !since.IsZero() && entry.At.Before(since) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.Message+" "+entry.Details+" "+entry.Component+" "+component.Title), search) {
			continue
		}
		total++
		if before != 0 && entry.ID >= before {
			continue
		}
		if !export && len(items) >= limit {
			more = true
			continue
		}
		items = append(items, logItem{Entry: entry, Feature: component.Feature.ID, ComponentTitle: component.Title})
	}
	w.Header().Set("Cache-Control", "no-store")
	if export {
		w.Header().Set("Content-Disposition", `attachment; filename="tdl-logs.json"`)
		writeJSON(w, http.StatusOK, items)
		return
	}
	debug := false
	if cfg := config.From(s.opts.Context); cfg != nil {
		debug = cfg.Debug
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "components": components, "total": total, "has_more": more, "retained": retained, "capacity": logging.Capacity, "warning": logging.Redact(warning), logDebugLevel: debug})
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

const logDebugLevel = "debug"
