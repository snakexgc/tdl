package logging

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	credentialsURL   = regexp.MustCompile(`(?i)(https?|socks5h?)://[^\s/@]+:[^\s/@]+@`)
	botToken         = regexp.MustCompile(`\b(?:bot)?[0-9]{5,}:[A-Za-z0-9_-]{20,}\b`)
	secretAssignment = regexp.MustCompile(`(?i)(token|password|api_hash|secret|authorization)(["\s:=]+)[^\s,;"}]+`)
)

func Redact(value string) string {
	value = credentialsURL.ReplaceAllString(value, "$1://[REDACTED]@")
	value = botToken.ReplaceAllString(value, "[REDACTED]")
	value = secretAssignment.ReplaceAllString(value, "$1$2[REDACTED]")
	return bounded(value, 8192)
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}

func safeValue(key string, value any) any {
	lower := strings.ToLower(key)
	for _, secret := range []string{"password", "token", "secret", "api_hash", "app_hash", "authkey", "auth_key", "authorization", "session", "phone", "code", "payload", "body", "text", "request", "response"} {
		if strings.Contains(lower, secret) {
			return "[REDACTED]"
		}
	}
	switch item := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for name, v := range item {
			result[name] = safeValue(name, v)
		}
		return result
	case []any:
		result := make([]any, 0, len(item))
		for _, v := range item {
			result = append(result, safeValue("", v))
		}
		return result
	case string:
		return Redact(item)
	case error:
		return Redact(item.Error())
	case nil, bool, float64:
		return item
	default:
		data, err := json.Marshal(value)
		var normalized any
		if err == nil && json.Unmarshal(data, &normalized) == nil {
			return safeValue("", normalized)
		}
		return Redact(fmt.Sprint(value))
	}
}

type Core struct {
	store  *Store
	level  zapcore.LevelEnabler
	fields []zap.Field
}

func NewCore(store *Store, level zapcore.LevelEnabler) zapcore.Core {
	return &Core{store: store, level: level}
}
func (c *Core) Enabled(level zapcore.Level) bool { return c.level.Enabled(level) }
func (c *Core) With(fields []zap.Field) zapcore.Core {
	next := *c
	next.fields = append(append([]zap.Field{}, c.fields...), fields...)
	return &next
}

func (c *Core) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}
func (c *Core) Sync() error { return nil }
func (c *Core) Write(entry zapcore.Entry, fields []zap.Field) error {
	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range c.fields {
		field.AddTo(encoder)
	}
	for _, field := range fields {
		field.AddTo(encoder)
	}
	str := func(key string) string {
		value := encoder.Fields[key]
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
	item := Entry{At: entry.Time, Level: entry.Level.String(), Account: str("account"), Component: str("component"), Kind: str("kind"), Message: entry.Message, Logger: entry.LoggerName, Caller: entry.Caller.TrimmedPath()}
	if item.Kind == "" {
		item.Kind = "runtime"
	}
	if item.Component == "" {
		item.Component = "system"
	}
	safe := map[string]any{}
	for key, value := range encoder.Fields {
		if key != "account" && key != "component" && key != "kind" {
			safe[key] = safeValue(key, value)
		}
	}
	if len(safe) > 0 {
		data, err := json.Marshal(safe)
		if err == nil {
			item.Details = bounded(string(data), 16384)
		}
	}
	return c.store.Write(item)
}
