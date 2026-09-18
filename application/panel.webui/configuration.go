package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type ConfigurationService struct {
	store ports.ConfigurationStore
	owned func() bool
	mu    sync.Mutex
}

func NewConfiguration(store ports.ConfigurationStore, owned func() bool) *ConfigurationService {
	return &ConfigurationService{store: store, owned: owned}
}

func cloneConfiguration(cfg *types.RuntimeConfig) (*types.RuntimeConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var copy types.RuntimeConfig
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil, err
	}
	return &copy, nil
}

func (s *ConfigurationService) Read(ctx context.Context) (*types.RuntimeConfig, error) {
	value, err := s.store.Read(ctx)
	if err != nil {
		return nil, err
	}
	return PublicConfig(value), nil
}

func (s *ConfigurationService) Patch(ctx context.Context, values map[string]json.RawMessage) (*types.RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := s.store.Read(ctx)
	if err != nil {
		return nil, err
	}
	next, err := cloneConfiguration(before)
	if err != nil {
		return nil, err
	}
	owned := s.owned != nil && s.owned()
	for path, raw := range values {
		if owned && componentPolicyPath(path) {
			return nil, errors.New("edit migrated settings in the component configuration page")
		}
		if strings.EqualFold(strings.TrimSpace(path), "namespace") {
			return nil, errors.New("namespace must be changed from user management")
		}
		if isBlankWebUIUsernamePatch(path, raw) {
			return nil, errors.New("webui.username cannot be blank")
		}
		if IsBlankSensitivePatch(path, raw) {
			continue
		}
		if err := setConfigJSONValue(next, path, raw); err != nil {
			return nil, fmt.Errorf("set %s: %w", path, err)
		}
	}
	if next.Namespace != before.Namespace {
		return nil, errors.New("namespace must be changed from user management")
	}
	if strings.TrimSpace(next.WebUI.Username) == "" {
		return nil, errors.New("webui.username cannot be blank")
	}
	if err := s.store.Validate(next); err != nil {
		return nil, err
	}
	if owned && (!reflect.DeepEqual(next.Bot.AllowedUsers, before.Bot.AllowedUsers) || !reflect.DeepEqual(next.Telegram, before.Telegram) || !reflect.DeepEqual(next.TriggerReactions, before.TriggerReactions) || !reflect.DeepEqual(next.Forward.TriggerReactions, before.Forward.TriggerReactions) ||
		!reflect.DeepEqual(next.Include, before.Include) || !reflect.DeepEqual(next.Exclude, before.Exclude) || next.FileSizeMinMB != before.FileSizeMinMB || next.FileSizeMaxMB != before.FileSizeMaxMB || next.Filename != before.Filename || next.FilenameMax != before.FilenameMax || next.DownloadDir != before.DownloadDir) {
		return nil, errors.New("edit account, reaction and permission settings in the component configuration page")
	}
	if err := s.store.Save(ctx, before, next); err != nil {
		return nil, err
	}
	return PublicConfig(next), nil
}

func PublicConfig(cfg *types.RuntimeConfig) *types.RuntimeConfig {
	next, err := cloneConfiguration(cfg)
	if err != nil {
		next = &types.RuntimeConfig{}
	}
	next.Bot.Token = ""
	next.Aria2.Secret = ""
	next.WebUI.Password = ""
	next.ProxyPassword = ""
	next.Telegram.APIHash = ""
	return next
}

func isBlankWebUIUsernamePatch(path string, raw json.RawMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(path), "webui.username") {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) == ""
}

func IsBlankSensitivePatch(path string, raw json.RawMessage) bool {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "bot.token", "aria2.secret", "webui.password", "proxy_password", "telegram.api_hash":
	default:
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == ""
}

func setConfigJSONValue(cfg *types.RuntimeConfig, path string, raw json.RawMessage) error {
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

func componentPolicyPath(path string) bool {
	switch strings.ToLower(strings.Join(splitConfigPath(path), ".")) {
	case "include", "exclude", "file_size_min_mb", "file_size_max_mb", "filename", "download_dir", "filename_max_length", "filenamemax", "trigger_reactions", "forward.trigger_reactions", "telegram", "telegram.api_id", "telegram.api_hash", "telegram.builtin_preset", "telegram.use_builtin":
		return true
	default:
		return false
	}
}
