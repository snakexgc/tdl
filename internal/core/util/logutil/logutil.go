package logutil

import (
	"errors"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/snakexgc/tdl/bsw/services/logging"
)

func New(level zapcore.LevelEnabler, path string) *zap.Logger {
	logger, _ := NewWithClose(level, path)
	return logger
}

// NewWithClose lets the process release the rotating log before resetting state.
func NewWithClose(level zapcore.LevelEnabler, path string) (*zap.Logger, func() error) {
	logger, _, closeLog := NewSession(level, path, "")
	return logger, closeLog
}

func NewSession(level zapcore.LevelEnabler, path, account string) (*zap.Logger, *logging.Store, func() error) {
	rotate := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    10,
		MaxAge:     7,
		MaxBackups: 3,
		LocalTime:  true,
		Compress:   true,
	}

	writer := zapcore.AddSync(rotate)

	config := zap.NewDevelopmentEncoderConfig()
	config.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	config.EncodeLevel = zapcore.CapitalLevelEncoder

	journal := &lumberjack.Logger{Filename: filepath.Join(filepath.Dir(path), "events.jsonl"), MaxSize: 10, MaxAge: 7, MaxBackups: 3, LocalTime: true, Compress: true}
	store := logging.New(journal)
	restoreErr := store.Restore(journal.Filename)
	// Separate a possible incomplete final record left by an interrupted write.
	_, separatorErr := journal.Write([]byte("\n"))
	restoreErr = errors.Join(restoreErr, separatorErr)
	core := zapcore.NewTee(zapcore.NewCore(zapcore.NewConsoleEncoder(config), writer, level), logging.NewCore(store, level))
	logger := zap.New(core, zap.AddCaller()).With(zap.String("account", account))
	if restoreErr != nil {
		logger.Warn("读取日志历史失败", zap.Error(restoreErr))
	}
	return logger, store, func() error { return errors.Join(rotate.Close(), journal.Close()) }
}
