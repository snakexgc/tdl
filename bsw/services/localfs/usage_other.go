//go:build !windows

package localfs

import (
	"context"

	"github.com/shirou/gopsutil/v3/disk"
)

func volumeUsage(ctx context.Context, path string) (uint64, uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	usage, err := disk.UsageWithContext(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	return usage.Total, usage.Free, nil
}
