package timesync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

var builtinServers = []string{
	"cn.pool.ntp.org", "ntp.aliyun.com", "ntp.tencent.com", "ntp.sjtu.edu.cn", "ntp.nju.edu.cn",
	"time1.google.com", "time1.apple.com", "time.cloudflare.com", "time.windows.com",
}

func selectServer(ctx context.Context, preferred string, candidates []string, probe ports.TimeProbe, timeout time.Duration) (string, types.TimeSample, error) {
	query := func(host string) (sample types.TimeSample, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("NTP probe panic: %v", recovered)
			}
		}()
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		sample, err = probe.Query(bounded, host, timeout)
		if bounded.Err() != nil {
			return types.TimeSample{}, bounded.Err()
		}
		return sample, err
	}
	if preferred != "" {
		for range 3 {
			if err := ctx.Err(); err != nil {
				return "", types.TimeSample{}, err
			}
			if sample, err := query(preferred); err == nil {
				return preferred, sample, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", types.TimeSample{}, err
	}
	type result struct {
		host   string
		sample types.TimeSample
	}
	results := make(chan result, len(candidates))
	var wg sync.WaitGroup
	for _, host := range candidates {
		if host == preferred {
			continue
		}
		wg.Go(func() {
			if sample, err := query(host); err == nil {
				results <- result{host, sample}
			}
		})
	}
	wg.Wait()
	close(results)
	if err := ctx.Err(); err != nil {
		return "", types.TimeSample{}, err
	}
	var best result
	for next := range results {
		if best.host == "" || next.sample.Elapsed < best.sample.Elapsed || (next.sample.Elapsed == best.sample.Elapsed && next.host < best.host) {
			best = next
		}
	}
	if best.host == "" {
		return "", types.TimeSample{}, fmt.Errorf("no NTP server is reachable")
	}
	return best.host, best.sample, nil
}
