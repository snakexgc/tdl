package logging

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/snakexgc/tdl/interfaces/ports"
)

// Handler routes slog through the same level gate and sinks as Zap.
type Handler struct {
	logger *zap.Logger
	groups []string // Delay opening empty groups until an attribute is added.
	clock  ports.Clock
}

func Slog(logger *zap.Logger, clocks ...ports.Clock) *slog.Logger {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := &Handler{logger: logger}
	if len(clocks) > 0 {
		handler.clock = clocks[0]
	}
	return slog.New(handler)
}

// Custom slog levels must never invoke Zap's panic or process-exit behavior.
func slogLevel(level slog.Level) zapcore.Level {
	switch {
	case level < slog.LevelInfo:
		return zap.DebugLevel
	case level < slog.LevelWarn:
		return zap.InfoLevel
	case level < slog.LevelError:
		return zap.WarnLevel
	default:
		return zap.ErrorLevel
	}
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return h.logger.Core().Enabled(slogLevel(level))
}

func (h *Handler) Handle(_ context.Context, record slog.Record) error {
	checked := h.logger.Check(slogLevel(record.Level), record.Message)
	if checked == nil {
		return nil
	}
	checked.Time = record.Time
	if h.clock != nil {
		checked.Time = h.clock.Now().In(time.Local)
	}
	checked.Caller = zapcore.EntryCaller{}
	if record.PC != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
		checked.Caller = zapcore.EntryCaller{Defined: frame.File != "", PC: record.PC, File: frame.File, Line: frame.Line, Function: frame.Function}
	}
	fields := make([]zap.Field, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool { fields = appendSlogAttr(fields, attr); return true })
	checked.Write(h.grouped(fields)...)
	return nil
}

func appendSlogAttr(fields []zap.Field, attr slog.Attr) []zap.Field {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return fields
	}
	if attr.Value.Kind() == slog.KindGroup {
		children := []zap.Field{}
		for _, child := range attr.Value.Group() {
			children = appendSlogAttr(children, child)
		}
		if len(children) == 0 {
			return fields
		}
		if attr.Key == "" {
			return append(fields, children...)
		}
		return append(fields, zap.Object(attr.Key, slogObject(children)))
	}
	return append(fields, zap.Any(attr.Key, attr.Value.Any()))
}

type slogObject []zap.Field

func (fields slogObject) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for _, field := range fields {
		field.AddTo(enc)
	}
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make([]zap.Field, 0, len(attrs))
	for _, attr := range attrs {
		fields = appendSlogAttr(fields, attr)
	}
	if len(fields) == 0 {
		return h
	}
	return &Handler{logger: h.logger.With(h.grouped(fields)...), clock: h.clock}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{logger: h.logger, groups: append(append([]string{}, h.groups...), name), clock: h.clock}
}

func (h *Handler) grouped(fields []zap.Field) []zap.Field {
	if len(fields) == 0 || len(h.groups) == 0 {
		return fields
	}
	result := make([]zap.Field, 0, len(h.groups)+len(fields))
	for _, name := range h.groups {
		result = append(result, zap.Namespace(name))
	}
	return append(result, fields...)
}
