package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/pkg/config"
)

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
		next, err := config.Clone(config.Get())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for path, raw := range req.Values {
			if strings.EqualFold(strings.TrimSpace(path), "namespace") {
				writeError(w, http.StatusBadRequest, errors.New("namespace must be changed from user management"))
				return
			}
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
			"ok":         true,
			"config":     publicConfig(next),
			fieldMessage: "配置已保存。模块开关会立即生效；监听地址、命名空间、Bot Token 等基础连接参数建议重启后再使用。",
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
	info, err := updater.CheckLatest(r.Context(), config.EffectiveProxy(config.Get()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
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
	plan, info, err := updater.DownloadLatest(r.Context(), config.EffectiveProxy(config.Get()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"update":     info,
		fieldMessage: fmt.Sprintf("更新包已下载，准备更新到 %s 并重启。", info.LatestVersion),
	})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestUpdate(plan)
	}()
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldMessage: "正在重启 tdl"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestReboot()
	}()
}

func publicConfig(cfg *config.Config) *config.Config {
	next, err := config.Clone(cfg)
	if err != nil {
		next = config.DefaultConfig()
	}
	next.Bot.Token = ""
	next.Aria2.Secret = ""
	next.WebUI.Password = ""
	next.ProxyPassword = ""
	return next
}

func isBlankSensitivePatch(path string, raw json.RawMessage) bool {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "bot.token", "aria2.secret", "webui.password", "proxy_password":
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
