package ps

import (
	"context"
	"os"
	"runtime"

	"github.com/shirou/gopsutil/v3/process"
)

var proc *process.Process

func init() {
	var err error
	proc, err = process.NewProcess(int32(os.Getpid()))
	if err != nil {
		panic(err)
	}
}

func GetSelfCPU(ctx context.Context) (float64, error) {
	cpu, err := proc.PercentWithContext(ctx, 0)
	if err != nil {
		return 0, err
	}

	return cpu, nil
}

// GetSelfMem returns self memory info
func GetSelfMem(ctx context.Context) (*process.MemoryInfoStat, error) {
	m, err := proc.MemoryInfoWithContext(ctx)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func GetGoroutineNum() int {
	return runtime.NumGoroutine()
}
