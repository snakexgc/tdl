package bot

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/pkg/config"
)

var configurablePaths = []string{
	"storage.type",
	"storage.path",
	"proxy",
	"namespace",
	"debug",
	"threads",
	"limit",
	"pool_size",
	"delay",
	"ntp",
	"reconnect_timeout",
	"download_dir",
	"include",
	"exclude",
	"http.listen",
	"http.public_base_url",
	"aria2.rpc_url",
	"aria2.secret",
	"aria2.dir",
	"aria2.timeout_seconds",
	"bot.allowed_users",
}

func handleConfigCommand(ctx *th.Context, msg *telego.Message, text string, afterSave func(*config.Config)) (bool, error) {
	cmd, _, payload := tu.ParseCommandPayload(text)
	switch "/" + cmd {
	case "/config", "/config_help":
		return true, sendMessage(ctx, msg.Chat.ID, configHelpMessage())
	case "/config_get":
		return true, handleConfigGet(ctx, msg.Chat.ID, strings.TrimSpace(payload))
	case "/config_set":
		return true, handleConfigSet(ctx, msg.Chat.ID, payload, afterSave)
	default:
		return false, nil
	}
}

func handleConfigGet(ctx *th.Context, chatID int64, path string) error {
	if path == "" {
		return sendMessage(ctx, chatID, "当前配置：\n"+formatJSON(maskedConfig(config.Get())))
	}
	if isProtectedConfigPath(path) {
		return sendMessage(ctx, chatID, "bot.token 不能通过机器人查看或修改。")
	}

	value, err := getConfigValue(config.Get(), path)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("读取配置失败：%v", err))
	}
	return sendMessage(ctx, chatID, fmt.Sprintf("%s = %s", path, formatJSON(value)))
}

func handleConfigSet(ctx *th.Context, chatID int64, payload string, afterSave func(*config.Config)) error {
	path, raw, ok := splitConfigSetPayload(payload)
	if !ok {
		return sendMessage(ctx, chatID, "用法：/config_set 配置项 值\n例如：/config_set limit 3\n例如：/config_set include [\"mp4\",\"mkv\"]")
	}
	if isProtectedConfigPath(path) {
		return sendMessage(ctx, chatID, "bot.token 不能通过机器人修改，请继续在 config.json 中维护。")
	}

	next, err := cloneConfig(config.Get())
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("复制配置失败：%v", err))
	}
	if err := setConfigValue(next, path, raw); err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("保存配置失败：%v", err))
	}
	if err := config.Set(next); err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("写入 config.json 失败：%v", err))
	}
	if afterSave != nil {
		afterSave(next)
	}

	value, _ := getConfigValue(next, path)
	return sendMessage(ctx, chatID, fmt.Sprintf("配置已保存：%s = %s\n当前运行中的 watch 可能需要 /reboot 后完整生效。", path, formatJSON(value)))
}

func splitConfigSetPayload(payload string) (path, raw string, ok bool) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", "", false
	}

	fields := strings.Fields(payload)
	if len(fields) < 2 {
		return "", "", false
	}

	path = fields[0]
	raw = strings.TrimSpace(payload[len(path):])
	return path, raw, raw != ""
}

func configHelpMessage() string {
	paths := append([]string(nil), configurablePaths...)
	sort.Strings(paths)

	return fmt.Sprintf("配置命令：\n/config_get 查看全部配置\n/config_get 配置项 查看单项配置\n/config_set 配置项 值 保存配置\n/reboot 重启程序并重新加载配置\n\n值支持 JSON；字符串也可以直接写。\ndownload_dir 支持 G/I/Y/M/D，/ 或 \\ 分层，& 连接同层，例如 Y&M/I/G。\n配置文件：%s\n\n可设置配置项：\n%s", config.GetPath(), strings.Join(paths, "\n"))
}

func isProtectedConfigPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	return path == "bot" || path == "bot.token" || strings.HasPrefix(path, "bot.token.")
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

func maskedConfig(cfg *config.Config) *config.Config {
	next, err := cloneConfig(cfg)
	if err != nil {
		return config.DefaultConfig()
	}
	if next.Bot.Token != "" {
		next.Bot.Token = "(hidden)"
	}
	return next
}

func getConfigValue(cfg *config.Config, path string) (any, error) {
	value, err := getPathValue(reflect.ValueOf(cfg).Elem(), splitConfigPath(path))
	if err != nil {
		return nil, err
	}
	return value.Interface(), nil
}

func setConfigValue(cfg *config.Config, path, raw string) error {
	return setPathValue(reflect.ValueOf(cfg).Elem(), splitConfigPath(path), raw)
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

func getPathValue(value reflect.Value, path []string) (reflect.Value, error) {
	value = indirectValue(value)
	if len(path) == 0 {
		return value, nil
	}

	switch value.Kind() {
	case reflect.Struct:
		field, ok := fieldByJSONName(value, path[0])
		if !ok {
			return reflect.Value{}, fmt.Errorf("未知配置项 %q", path[0])
		}
		return getPathValue(field, path[1:])
	case reflect.Map:
		if len(path) != 1 {
			return reflect.Value{}, fmt.Errorf("配置项 %q 不是对象", path[0])
		}
		key, err := mapKeyValue(value.Type().Key(), path[0])
		if err != nil {
			return reflect.Value{}, err
		}
		item := value.MapIndex(key)
		if !item.IsValid() {
			return reflect.Value{}, fmt.Errorf("未知配置项 %q", path[0])
		}
		return item, nil
	default:
		return reflect.Value{}, fmt.Errorf("配置项 %q 不能继续展开", path[0])
	}
}

func setPathValue(value reflect.Value, path []string, raw string) error {
	value = indirectValue(value)
	if len(path) == 0 {
		return fmt.Errorf("配置项不能为空")
	}

	switch value.Kind() {
	case reflect.Struct:
		field, ok := fieldByJSONName(value, path[0])
		if !ok {
			return fmt.Errorf("未知配置项 %q", path[0])
		}
		if len(path) == 1 {
			next, err := parseConfigValue(raw, field.Type())
			if err != nil {
				return err
			}
			if !field.CanSet() {
				return fmt.Errorf("配置项 %q 不能修改", path[0])
			}
			field.Set(next)
			return nil
		}
		return setPathValue(field, path[1:], raw)
	case reflect.Map:
		if len(path) != 1 {
			return fmt.Errorf("配置项 %q 不是对象", path[0])
		}
		key, err := mapKeyValue(value.Type().Key(), path[0])
		if err != nil {
			return err
		}
		next, err := parseConfigValue(raw, value.Type().Elem())
		if err != nil {
			return err
		}
		if value.IsNil() {
			if !value.CanSet() {
				return fmt.Errorf("配置项 %q 不能修改", path[0])
			}
			value.Set(reflect.MakeMap(value.Type()))
		}
		value.SetMapIndex(key, next)
		return nil
	default:
		return fmt.Errorf("配置项 %q 不能继续展开", path[0])
	}
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
		return reflect.Value{}, fmt.Errorf("不支持的 map key 类型 %s", typ)
	}
}

func parseConfigValue(raw string, typ reflect.Type) (reflect.Value, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return reflect.Value{}, fmt.Errorf("配置值不能为空")
	}

	switch typ.Kind() {
	case reflect.String:
		return parseStringValue(raw, typ)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.String && !strings.HasPrefix(raw, "[") {
			return reflect.ValueOf(splitStringList(raw)).Convert(typ), nil
		}
		if typ.Elem().Kind() == reflect.Int64 && !strings.HasPrefix(raw, "[") {
			values, err := splitInt64List(raw)
			if err != nil {
				return reflect.Value{}, err
			}
			return reflect.ValueOf(values).Convert(typ), nil
		}
	default:
		// 其他类型通过 JSON 解析处理
	}

	target := reflect.New(typ)
	if err := json.Unmarshal([]byte(raw), target.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("值格式错误，请使用 JSON 或合适的原始值：%w", err)
	}
	return target.Elem(), nil
}

func parseStringValue(raw string, typ reflect.Type) (reflect.Value, error) {
	if strings.HasPrefix(raw, "\"") {
		var decoded string
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return reflect.Value{}, fmt.Errorf("字符串 JSON 格式错误：%w", err)
		}
		return reflect.ValueOf(decoded).Convert(typ), nil
	}
	return reflect.ValueOf(raw).Convert(typ), nil
}

func splitStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitInt64List(raw string) ([]int64, error) {
	parts := splitStringList(raw)
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("整数列表格式错误：%w", err)
		}
		out = append(out, value)
	}
	return out, nil
}

func formatJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func sendMessage(ctx *th.Context, chatID int64, text string) error {
	_, err := ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), text))
	return err
}
