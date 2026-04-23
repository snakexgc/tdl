package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/kv"
)

type cleanKVResult struct {
	Namespace string
	Deleted   int
	Kept      int
	Errors    []string
}

func cleanCurrentNamespaceKV(ctx context.Context, engine kv.Storage, namespace string, namespaceKV storage.Storage) (cleanKVResult, error) {
	result := cleanKVResult{Namespace: namespace}
	if engine == nil {
		return result, fmt.Errorf("kv engine is not configured")
	}
	if namespace == "" {
		return result, fmt.Errorf("namespace is empty")
	}
	if namespaceKV == nil {
		return result, fmt.Errorf("namespace kv storage is not configured")
	}

	meta, err := engine.MigrateTo()
	if err != nil {
		return result, fmt.Errorf("list kv keys: %w", err)
	}

	pairs := meta[namespace]
	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if preserveKVKey(key) {
			result.Kept++
			continue
		}
		if err := namespaceKV.Delete(ctx, key); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		result.Deleted++
	}
	return result, nil
}

func preserveKVKey(key string) bool {
	switch key {
	case "session", "app":
		return true
	}

	for _, prefix := range []string{
		"peers:",
		"state:",
		"chan:",
	} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func formatCleanKVResult(result cleanKVResult) string {
	parts := []string{
		"KV 清理完成。",
		"命名空间：" + result.Namespace,
		fmt.Sprintf("已删除缓存：%d", result.Deleted),
		fmt.Sprintf("已保留登录/状态信息：%d", result.Kept),
		"保留范围：session, app, peers:*, state:*, chan:*",
	}
	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("删除失败：%d", len(result.Errors)))
		for _, line := range firstStrings(result.Errors, 5) {
			parts = append(parts, "- "+line)
		}
	}
	return strings.Join(parts, "\n")
}
