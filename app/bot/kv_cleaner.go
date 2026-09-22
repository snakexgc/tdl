package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

type cleanKVResult = ports.CleanupResult

func cleanCurrentNamespaceKV(ctx context.Context, engine kv.Storage, namespace string, namespaceKV storage.Storage) (cleanKVResult, error) {
	host, port, err := application.MaintenanceHost(ctx, types.AccountID(namespace), taskhub.CleanupRepository{Engine: engine, Namespace: namespace, Store: namespaceKV})
	if err != nil {
		return cleanKVResult{}, err
	}
	defer host.Stop(context.Background())
	return port.Clean(ctx, types.AccountID(namespace))
}

func formatCleanKVResult(result cleanKVResult) string {
	parts := []string{
		"KV 清理完成。",
		"命名空间：" + result.Namespace,
		fmt.Sprintf("已删除缓存：%d", result.Deleted),
		fmt.Sprintf("已保留登录/状态信息：%d", result.Kept),
		"保留范围：session, app, peers:*, state:*, chan:*, access_hash:*",
	}
	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("删除失败：%d", len(result.Errors)))
		for _, line := range firstStrings(result.Errors, 5) {
			parts = append(parts, "- "+line)
		}
	}
	return strings.Join(parts, "\n")
}
