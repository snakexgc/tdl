package localfs

import (
	"context"

	"golang.org/x/sys/windows"
)

func volumeUsage(ctx context.Context, path string) (total, free uint64, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	// Use bytes available to this user, respecting Windows disk quotas. The
	// API resolves drive letters, mounted directories and UNC shares itself.
	var allFree uint64
	err = windows.GetDiskFreeSpaceEx(name, &free, &total, &allFree)
	return
}
