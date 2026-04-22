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
	if l.shuttingDown.Load() && isTelegoShutdownNoise(msg) {
		return
	}
	if l.replacer != nil {
		msg = l.replacer.Replace(msg)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.out, "[%s] ERROR %s\n", time.Now().Format(time.UnixDate), msg)
}

func isTelegoShutdownNoise(msg string) bool {
	lower := strings.ToLower(msg)
	if strings.HasPrefix(lower, "retrying getting updates") {
		return true
	}
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
