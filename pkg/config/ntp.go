package config

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/beevik/ntp"
	"github.com/go-faster/errors"
)

const (
	NTPQueryTimeout       = 3 * time.Second
	ConfiguredNTPMaxTries = 3
)

var BuiltinNTPServers = []string{
	"cn.pool.ntp.org",
	"ntp.aliyun.com",
	"ntp.tencent.com",
	"ntp.sjtu.edu.cn",
	"ntp.nju.edu.cn",
	"time1.google.com",
	"time1.apple.com",
	"time.cloudflare.com",
	"time.windows.com",
}

type NTPSelection struct {
	Host             string
	Elapsed          time.Duration
	Source           string
	Saved            bool
	ConfiguredFailed bool
}

type ntpProbeFunc func(ctx context.Context, host string, timeout time.Duration) (time.Duration, error)

var probeNTP = queryNTP

func SelectAndSaveStartupNTP(ctx context.Context) (NTPSelection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	mu.RLock()
	if instance == nil {
		mu.RUnlock()
		return NTPSelection{}, nil
	}
	current := strings.TrimSpace(instance.NTP)
	path := configPath
	mu.RUnlock()

	selection := selectStartupNTP(ctx, current, BuiltinNTPServers, probeNTP)
	if err := ctx.Err(); err != nil {
		return selection, err
	}

	mu.Lock()
	defer mu.Unlock()
	if instance == nil {
		return selection, nil
	}

	nextHost := selection.Host
	if strings.TrimSpace(instance.NTP) == nextHost {
		return selection, nil
	}

	instance.NTP = nextHost
	if path == "" {
		path = configPath
	}
	if path == "" {
		return selection, nil
	}
	if err := Save(path, instance); err != nil {
		return selection, err
	}
	selection.Saved = true
	return selection, nil
}

func selectStartupNTP(ctx context.Context, configured string, builtin []string, probe ntpProbeFunc) NTPSelection {
	if ctx == nil {
		ctx = context.Background()
	}
	configured = strings.TrimSpace(configured)
	if configured != "" {
		for i := 0; i < ConfiguredNTPMaxTries; i++ {
			elapsed, err := probe(ctx, configured, NTPQueryTimeout)
			if err == nil {
				return NTPSelection{
					Host:    configured,
					Elapsed: elapsed,
					Source:  "configured",
				}
			}
			if ctx.Err() != nil {
				return NTPSelection{
					ConfiguredFailed: true,
				}
			}
		}
	}

	selection := selectFastestBuiltinNTP(ctx, builtin, probe)
	selection.ConfiguredFailed = configured != "" && selection.Host == ""
	if configured != "" && selection.Host != "" {
		selection.ConfiguredFailed = true
	}
	return selection
}

func selectFastestBuiltinNTP(ctx context.Context, servers []string, probe ntpProbeFunc) NTPSelection {
	if ctx == nil {
		ctx = context.Background()
	}
	hosts := normalizeNTPHosts(servers)
	if len(hosts) == 0 {
		return NTPSelection{Source: "system"}
	}

	results := make(chan NTPSelection, len(hosts))
	var wg sync.WaitGroup
	wg.Add(len(hosts))
	// Probe built-in servers in parallel so startup latency is capped by the
	// slowest single request timeout, not by the number of configured hosts.
	for _, host := range hosts {
		host := host
		go func() {
			defer wg.Done()
			elapsed, err := probe(ctx, host, NTPQueryTimeout)
			if err != nil {
				return
			}
			results <- NTPSelection{
				Host:    host,
				Elapsed: elapsed,
				Source:  "builtin",
			}
		}()
	}
	wg.Wait()
	close(results)

	var best NTPSelection
	for result := range results {
		if best.Host == "" || result.Elapsed < best.Elapsed {
			best = result
		}
	}
	if best.Host == "" {
		best.Source = "system"
	}
	return best
}

func normalizeNTPHosts(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func queryNTP(ctx context.Context, host string, timeout time.Duration) (time.Duration, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = NTPQueryTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	resp, err := ntp.QueryWithOptions(host, ntp.QueryOptions{
		Timeout: timeout,
		Dialer: func(localAddress, remoteAddress string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			if localAddress != "" {
				local, err := net.ResolveUDPAddr("udp", net.JoinHostPort(localAddress, "0"))
				if err != nil {
					return nil, err
				}
				dialer.LocalAddr = local
			}
			return dialer.DialContext(queryCtx, "udp", remoteAddress)
		},
	})
	elapsed := time.Since(start)
	if err != nil {
		return elapsed, err
	}
	if resp == nil {
		return elapsed, errors.New("empty ntp response")
	}
	if err := resp.Validate(); err != nil {
		return elapsed, err
	}
	if queryCtx.Err() != nil {
		return elapsed, queryCtx.Err()
	}
	return elapsed, nil
}

func FormatNTPSelection(selection NTPSelection) string {
	if selection.Host == "" {
		return "system time"
	}
	if selection.Elapsed <= 0 {
		return selection.Host
	}
	return fmt.Sprintf("%s (%s)", selection.Host, selection.Elapsed.Round(time.Millisecond))
}
