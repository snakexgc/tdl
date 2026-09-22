package logging

import (
	"fmt"
	"sort"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// fieldContext snapshots With fields immediately, and preserves open namespaces.
// Using Zap's encoder also honors ObjectMarshaler, ArrayMarshaler and errors.
type fieldContext struct{ encoder zapcore.Encoder }

func newFieldContext() fieldContext {
	return fieldContext{encoder: zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		EncodeTime: zapcore.ISO8601TimeEncoder, EncodeDuration: zapcore.NanosDurationEncoder,
	})}
}

func (c fieldContext) with(fields []zap.Field) fieldContext {
	next := fieldContext{encoder: c.encoder.Clone()}
	for _, field := range fields {
		field.AddTo(next.encoder)
	}
	return next
}

func (c fieldContext) values(fields []zap.Field) (map[string]any, error) {
	buffer, err := c.encoder.EncodeEntry(zapcore.Entry{}, fields)
	if err != nil {
		return nil, err
	}
	defer buffer.Free()
	var values map[string]any
	err = decodeJSON(buffer.Bytes(), &values)
	return values, err
}

// RedactingCore sanitizes before fan-out, so text and structured logs have the
// same confidentiality guarantees. It does not sample away state transitions.
type RedactingCore struct {
	zapcore.Core
	context fieldContext
}

func NewRedactingCore(core zapcore.Core) zapcore.Core {
	return &RedactingCore{Core: core, context: newFieldContext()}
}

func (c *RedactingCore) With(fields []zap.Field) zapcore.Core {
	return &RedactingCore{Core: c.Core, context: c.context.with(fields)}
}

func (c *RedactingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

func (c *RedactingCore) Write(entry zapcore.Entry, fields []zap.Field) error {
	values, err := c.context.values(fields)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	safe := make([]zap.Field, 0, len(keys))
	for _, key := range keys {
		safe = append(safe, zap.Any(key, safeValue(key, values[key])))
	}
	entry.Message = Redact(entry.Message)
	entry.LoggerName = Redact(entry.LoggerName)
	entry.Stack = Redact(entry.Stack)
	return c.Core.Write(entry, safe)
}

type Core struct {
	store   *Store
	level   zapcore.LevelEnabler
	context fieldContext
}

func NewCore(store *Store, level zapcore.LevelEnabler) zapcore.Core {
	return &Core{store: store, level: level, context: newFieldContext()}
}

func (c *Core) Enabled(level zapcore.Level) bool { return c.level.Enabled(level) }
func (c *Core) With(fields []zap.Field) zapcore.Core {
	next := *c
	next.context = c.context.with(fields)
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
	values, err := c.context.values(fields)
	if err != nil {
		return err
	}
	str := func(key string) string {
		value := values[key]
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
	delete(values, "account")
	delete(values, "component")
	delete(values, "kind")
	if len(values) > 0 {
		item.Details = safeDetails(values)
	}
	return c.store.Write(item)
}
