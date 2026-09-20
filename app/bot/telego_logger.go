package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/snakexgc/tdl/bsw/services/logging"
)

const telegoTokenReplacement = "BOT_TOKEN"

type shutdownAwareTelegoLogger struct {
	logger       *slog.Logger
	replacer     *strings.Replacer
	shuttingDown atomic.Bool
}

func newShutdownAwareTelegoLogger(token string) *shutdownAwareTelegoLogger {
	logger := &shutdownAwareTelegoLogger{logger: slog.Default().With("component", "console.bot")}
	if token != "" {
		logger.replacer = strings.NewReplacer(token, telegoTokenReplacement)
	}
	return logger
}

func (l *shutdownAwareTelegoLogger) SetShuttingDown() {
	l.shuttingDown.Store(true)
}

// Telego debug messages include full request/response bodies and login input.
// Keep wire dumps disabled; recoverable transport errors are logged below.
func (l *shutdownAwareTelegoLogger) Debugf(_ string, _ ...any) {}

func (l *shutdownAwareTelegoLogger) Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if l.replacer != nil {
		msg = l.replacer.Replace(msg)
	}
	msg = logging.Redact(msg)
	if shouldSuppressTelegoError(msg, l.shuttingDown.Load()) {
		l.logger.Debug(msg)
		return
	}
	l.logger.Error(msg)
}

func shouldSuppressTelegoError(msg string, shuttingDown bool) bool {
	if isTelegoRetryNoise(msg) {
		return true
	}
	if shuttingDown && isTelegoShutdownNoise(msg) {
		return true
	}
	if isTelegoTransientUpdateNoise(msg) {
		return true
	}
	return false
}

func isTelegoRetryNoise(msg string) bool {
	return strings.HasPrefix(strings.ToLower(msg), "retrying getting updates")
}

func isTelegoShutdownNoise(msg string) bool {
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "getupdates") && !strings.Contains(lower, "getting updates") {
		return false
	}

	for _, marker := range []string{
		"context canceled",
		"context cancelled",
		"interrupt signal received",
		"operation was canceled",
		"operation was cancelled",
		"request canceled",
		"request cancelled",
		"use of closed network connection",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isTelegoTransientUpdateNoise(msg string) bool {
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "getupdates") && !strings.Contains(lower, "getting updates") {
		return false
	}

	for _, marker := range []string{
		": eof",
		" unexpected eof",
		" connection reset by peer",
		" broken pipe",
		" i/o timeout",
		" tls handshake timeout",
		" client.timeout exceeded",
		" no such host",
		" server misbehaving",
		" temporary failure in name resolution",
		" network is unreachable",
		" connection refused",
		" proxyconnect tcp",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}
