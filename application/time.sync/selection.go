package timesync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

var builtinServers = []string{
	"ntp.aliyun.com", "time1.cloud.tencent.com", "ntp.ntsc.ac.cn",
	"time.apple.com", "time.google.com", "time.windows.com",
	"time.cloudflare.com", "cn.pool.ntp.org", "ntp.sjtu.edu.cn", "ntp.nju.edu.cn",
}

func selectServer(ctx context.Context, candidates []string, probe ports.TimeProbe, timeout time.Duration) (string, types.TimeSample, error) {
	if err := ctx.Err(); err != nil {
		return "", types.TimeSample{}, err
	}
	// One deadline bounds the whole concurrent round, including DNS and dialing.
	round, cancel := context.WithTimeout(ctx, timeout)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	type result struct {
		host   string
		sample types.TimeSample
		err    error
	}
	hosts := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, host := range candidates {
		host = strings.TrimSpace(host)
		key := strings.ToLower(host)
		if host != "" && !seen[key] {
			seen[key] = true
			hosts = append(hosts, host)
		}
	}
	results := make(chan result, len(hosts))
	for _, host := range hosts {
		wg.Go(func() {
			next := result{host: host}
			defer func() {
				if recovered := recover(); recovered != nil {
					next.err = fmt.Errorf("NTP probe panic: %v", recovered)
				}
				results <- next
			}()
			next.sample, next.err = probe.Query(round, host, timeout)
		})
	}
	var failures []error
	for range hosts {
		select {
		case <-round.Done():
			return "", types.TimeSample{}, fmt.Errorf("NTP pool: %w", round.Err())
		case next := <-results:
			if err := round.Err(); err != nil {
				return "", types.TimeSample{}, err
			}
			if next.err == nil {
				return next.host, next.sample, nil
			}
			failures = append(failures, fmt.Errorf("%s: %w", next.host, next.err))
		}
	}
	return "", types.TimeSample{}, fmt.Errorf("no valid NTP response: %w", errors.Join(failures...))
}
