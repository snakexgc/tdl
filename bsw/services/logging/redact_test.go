package logging

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRedactSecretsWithoutHidingDiagnostics(t *testing.T) {
	const privateValue = "private-value"
	for _, input := range []string{
		`Authorization: Bearer private-value`,
		`Proxy-Authorization: Basic private-value`,
		`https://user:private-value@localhost/path?token=private-value&safe=yes`,
		`{"password":"private-value with spaces", "safe":"yes"}`,
		`password="private-value with spaces"`,
		`password='private-value with spaces'`,
		`password="private-value with \"escaped\" quotes"`,
		`https://private-value@localhost/`,
	} {
		out := Redact(input)
		require.NotContains(t, out, privateValue)
		require.NotContains(t, out, "with spaces")
		require.NotContains(t, out, "escaped")
		require.Equal(t, out, Redact(out), "redaction must be idempotent")
	}
	value := safeValue("", map[string]any{
		"request_id": "r1", "error_code": 42, "response_status": 503,
		"context": "safe", "botToken": privateValue,
		"phone_code_hash": privateValue, "request_body": privateValue, "session_id": privateValue,
		"nested": []any{map[string]any{"api_hash": privateValue, "password": privateValue}},
	})
	details := safeDetails(value)
	require.NotContains(t, details, privateValue)
	require.Contains(t, details, `"error_code":42`)
	require.Contains(t, details, `"request_id":"r1"`)
	require.Contains(t, details, `"response_status":503`)
	require.Contains(t, details, `"context":"safe"`)
}

func TestLogFieldsAreSnapshotsAndKeepIntegerPrecision(t *testing.T) {
	store := New(nil)
	value := map[string]any{"state": "before"}
	logger := zap.New(NewCore(store, zap.DebugLevel)).With(zap.Any("metadata", value), zap.Namespace("transfer"))
	value["state"] = "after"
	logger.Info("snapshot", zap.Uint64("size", 9007199254740993))
	entries, _ := store.Snapshot()
	require.Contains(t, entries[0].Details, `"state":"before"`)
	require.Contains(t, entries[0].Details, `"transfer":{"size":9007199254740993}`)
}

func TestOversizeDetailsRemainValidJSON(t *testing.T) {
	store := New(nil)
	logger := zap.New(NewCore(store, zap.InfoLevel))
	logger.Info("large", zap.Strings("values", []string{strings.Repeat("中", 6000), strings.Repeat("文", 6000), strings.Repeat("字", 6000)}))
	entries, _ := store.Snapshot()
	require.True(t, json.Valid([]byte(entries[0].Details)))
	require.LessOrEqual(t, len(entries[0].Details), 16384)
	require.Contains(t, entries[0].Details, `"truncated":true`)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestShortWritesAndRestoredSecrets(t *testing.T) {
	store := New(shortWriter{})
	require.ErrorIs(t, store.Write(Entry{Message: "retained"}), io.ErrShortWrite)
	entries, warning := store.Snapshot()
	require.Len(t, entries, 1)
	require.NotEmpty(t, warning)
	path := filepath.Join(t.TempDir(), "events.jsonl")
	entry, err := json.Marshal(Entry{ID: 20, Message: "password=private-value", Details: `{"password":"private-value", "size":9007199254740993}`})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(entry, '\n'), 0o600))
	restored := New(nil)
	require.NoError(t, restored.Restore(path))
	entries, _ = restored.Snapshot()
	require.NotContains(t, entries[0].Message+entries[0].Details, "private-value")
	require.Contains(t, entries[0].Details, "9007199254740993")
}
