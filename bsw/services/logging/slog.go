package logging

import (
	"context"
	"log/slog"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Handler struct {
	logger *zap.Logger
	groups []string
}

func Slog(logger *zap.Logger) *slog.Logger { return slog.New(&Handler{logger: logger}) }
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return h.logger.Core().Enabled(zapcore.Level(level / 4))
}

func (h *Handler) Handle(_ context.Context, record slog.Record) error {
	fields := []zap.Field{}
	record.Attrs(func(attr slog.Attr) bool { fields = append(fields, h.field(attr)); return true })
	h.logger.Log(zapcore.Level(record.Level/4), record.Message, fields...)
	return nil
}

func (h *Handler) field(attr slog.Attr) zap.Field {
	value := attr.Value.Resolve()
	key := attr.Key
	if len(h.groups) > 0 {
		key = strings.Join(h.groups, ".") + "." + key
	}
	if value.Kind() == slog.KindGroup {
		group := map[string]any{}
		for _, child := range value.Group() {
			group[child.Key] = child.Value.Resolve().Any()
		}
		return zap.Any(key, group)
	}
	return zap.Any(key, value.Any())
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make([]zap.Field, 0, len(attrs))
	for _, attr := range attrs {
		fields = append(fields, h.field(attr))
	}
	next := *h
	next.logger = h.logger.With(fields...)
	return &next
}

func (h *Handler) WithGroup(name string) slog.Handler {
	next := *h
	if name != "" {
		next.groups = append(append([]string{}, h.groups...), name)
	}
	return &next
}
