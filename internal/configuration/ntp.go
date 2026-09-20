package configuration

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/beevik/ntp"
	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/application"
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/rte"
)

const (
	ntpSourceConfigured   = "configured"
	ntpSourceBuiltin      = "builtin"
	ntpSourceSystem       = "system"
	ntpQueryTimeout       = 3 * time.Second
	configuredNTPMaxTries = 3
)

var builtinNTPServers = []string{
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

// SelectAndSaveStartupNTP preserves master cc2ea9a's selection policy, but writes
// through the unified repository before any runtime takes its startup snapshot.
// It is deliberately separate from Open/Install so offline tools and WebUI saves
// never probe the network or apply new settings to an already running service.
func SelectAndSaveStartupNTP(ctx context.Context, service *manager.Service) (NTPSelection, error) {
	return selectAndSaveStartupNTP(ctx, service, builtinNTPServers, queryNTP)
}

func selectAndSaveStartupNTP(ctx context.Context, service *manager.Service, servers []string, probe ntpProbeFunc) (NTPSelection, error) {
	catalog, err := application.Catalog()
	if err != nil {
		return NTPSelection{}, err
	}
	store := service.Store()
	revision, err := store.Revision(ctx, accounttelegram.ID)
	if err != nil {
		return NTPSelection{}, err
	}
	document, err := store.Load(ctx, accounttelegram.ID)
	if err != nil {
		return NTPSelection{}, err
	}
	view, err := catalog.View(ctx, accounttelegram.ID, document.Values)
	if err != nil {
		return NTPSelection{}, err
	}
	var current string
	if err := view.Get("ntp", &current); err != nil {
		return NTPSelection{}, err
	}
	selection := selectStartupNTP(ctx, current, servers, probe)
	if err := ctx.Err(); err != nil {
		return selection, err
	}
	if current == selection.Host {
		return selection, nil
	}
	// Check the original revision and patch only NTP, preserving every other
	// component/system setting and refusing to overwrite edits made while probing.
	directory := rte.NewDirectory(catalog, store)
	if err := directory.PatchWithRevision(ctx, accounttelegram.ID, map[string]any{"ntp": selection.Host}, revision); err != nil {
		return selection, err
	}
	selection.Saved = true
	return selection, nil
}

func selectStartupNTP(ctx context.Context, configured string, builtin []string, probe ntpProbeFunc) NTPSelection {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return NTPSelection{}
	}
	configured = strings.TrimSpace(configured)
	if configured != "" {
		for i := 0; i < configuredNTPMaxTries; i++ {
			elapsed, err := probe(ctx, configured, ntpQueryTimeout)
			if err == nil {
				return NTPSelection{
					Host:    configured,
					Elapsed: elapsed,
					Source:  ntpSourceConfigured,
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
	selection.ConfiguredFailed = configured != ""
	return selection
}

func selectFastestBuiltinNTP(ctx context.Context, servers []string, probe ntpProbeFunc) NTPSelection {
	if ctx == nil {
		ctx = context.Background()
	}
	hosts := normalizeNTPHosts(servers)
	if len(hosts) == 0 {
		return NTPSelection{Source: ntpSourceSystem}
	}

	results := make(chan NTPSelection, len(hosts))
	var wg sync.WaitGroup
	wg.Add(len(hosts))
	// Probe built-in servers in parallel so startup latency is capped by the
	// slowest single request timeout, not by the number of configured hosts.
	for _, host := range hosts {
		go func() {
			defer wg.Done()
			elapsed, err := probe(ctx, host, ntpQueryTimeout)
			if err != nil {
				return
			}
			results <- NTPSelection{
				Host:    host,
				Elapsed: elapsed,
				Source:  ntpSourceBuiltin,
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
		best.Source = ntpSourceSystem
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
		timeout = ntpQueryTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stopCancel func() bool
	defer func() {
		if stopCancel != nil {
			stopCancel()
		}
	}()

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
			conn, err := dialer.DialContext(queryCtx, "udp", remoteAddress)
			if err != nil {
				return nil, err
			}
			// The NTP library owns the connection deadline; closing on context
			// cancellation also interrupts an already pending UDP read.
			stopCancel = context.AfterFunc(queryCtx, func() { _ = conn.Close() })
			return conn, nil
		},
	})
	elapsed := time.Since(start)
	if queryCtx.Err() != nil {
		return elapsed, queryCtx.Err()
	}
	if err != nil {
		return elapsed, err
	}
	if resp == nil {
		return elapsed, errors.New("empty ntp response")
	}
	if err := resp.Validate(); err != nil {
		return elapsed, err
	}
	return elapsed, nil
}
