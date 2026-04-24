package bot

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const telegoTokenReplacement = "BOT_TOKEN"

type shutdownAwareTelegoLogger struct {
	out          io.Writer
	replacer     *strings.Replacer
	shuttingDown atomic.Bool
	mu           sync.Mutex
}

func newShutdownAwareTelegoLogger(token string) *shutdownAwareTelegoLogger {
	return &shutdownAwareTelegoLogger{
		out:      os.Stderr,
		replacer: strings.NewReplacer(token, telegoTokenReplacement),
	}
}

func (l *shutdownAwareTelegoLogger) SetShuttingDown() {
	l.shuttingDown.Store(true)
}

func (l *shutdownAwareTelegoLogger) Debugf(_ string, _ ...any) {}

func (l *shutdownAwareTelegoLogger) Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if shouldSuppressTelegoError(msg, l.shuttingDown.Load()) {
		return
	}
	if l.replacer != nil {
		msg = l.replacer.Replace(msg)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.out, "[%s] ERROR %s\n", time.Now().Format(time.UnixDate), msg)
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
