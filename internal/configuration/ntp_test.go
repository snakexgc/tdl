package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	runtimeconfig "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

const (
	ntpCustom = "custom.ntp"
	ntpFast   = "fast.ntp"
	ntpSlow   = "slow.ntp"
)

func TestStartupNTPSavesUnifiedConfigurationBeforeRuntimeSnapshot(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	service, err := Open(ctx, home)
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, service.Store())
	require.NoError(t, directory.Patch(ctx, "panel.webui", map[string]any{"password": "keep-secret"}))
	require.NoError(t, directory.Patch(ctx, accounttelegram.ID, map[string]any{"dc_pool_size": 16}))
	require.NoError(t, directory.SetEnabled(ctx, "trigger.forward", true))
	path := filepath.Join(home, manager.Filename)
	read := func() manager.Document {
		t.Helper()
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		var doc manager.Document
		require.NoError(t, json.Unmarshal(data, &doc))
		return doc
	}
	before := read()
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		ntpSlow: {{elapsed: 50 * time.Millisecond}}, ntpFast: {{elapsed: 5 * time.Millisecond}},
	})
	selection, err := selectAndSaveStartupNTP(ctx, service, []string{ntpSlow, ntpFast}, probe.probe)
	require.NoError(t, err)
	require.True(t, selection.Saved)
	require.Equal(t, ntpFast, selection.Host)
	before.Components[accounttelegram.ID].Values["ntp"] = ntpFast
	require.Equal(t, before, read(), "only NTP should change in the unified file")
	store, err := Install(ctx, service)
	require.NoError(t, err)
	require.Equal(t, ntpFast, runtimeconfig.Get().NTP)
	require.Equal(t, 16, runtimeconfig.Get().PoolSize)
	ids := []string{}
	for _, definition := range catalog.Definitions() {
		ids = append(ids, definition.Manifest.ID)
	}
	active, err := store.Snapshot(ctx, ids)
	require.NoError(t, err)
	for _, entry := range rte.NewDirectory(catalog, store).WithActiveStore(active).Configurations(ctx) {
		require.Empty(t, entry.Error, entry.ID)
		require.Empty(t, entry.Changes, entry.ID)
	}
	value, err := active.Load(ctx, accounttelegram.ID)
	require.NoError(t, err)
	require.Equal(t, ntpFast, value.Values["ntp"])
	require.NoFileExists(t, filepath.Join(home, "config.json"))
}

func TestStartupNTPPersistenceOutcomes(t *testing.T) {
	timeoutErr := errors.New("timeout")
	for _, test := range []struct {
		name, configured, wantHost, wantSource string
		configuredResults                      []fakeNTPProbeResult
		builtinResult                          fakeNTPProbeResult
		saved, failed                          bool
	}{
		{name: "working custom is retained", configured: ntpCustom, configuredResults: []fakeNTPProbeResult{{elapsed: 20 * time.Millisecond}}, wantHost: ntpCustom, wantSource: ntpSourceConfigured},
		{name: "custom retries recover", configured: ntpCustom, configuredResults: []fakeNTPProbeResult{{err: timeoutErr}, {elapsed: 20 * time.Millisecond}}, wantHost: ntpCustom, wantSource: ntpSourceConfigured},
		{name: "failed custom is replaced", configured: ntpCustom, configuredResults: []fakeNTPProbeResult{{err: timeoutErr}, {err: timeoutErr}, {err: timeoutErr}}, builtinResult: fakeNTPProbeResult{elapsed: time.Millisecond}, wantHost: ntpFast, wantSource: ntpSourceBuiltin, saved: true, failed: true},
		{name: "all fail clears custom", configured: ntpCustom, configuredResults: []fakeNTPProbeResult{{err: timeoutErr}, {err: timeoutErr}, {err: timeoutErr}}, builtinResult: fakeNTPProbeResult{err: timeoutErr}, wantSource: ntpSourceSystem, saved: true, failed: true},
		{name: "empty and unavailable stays empty", builtinResult: fakeNTPProbeResult{err: timeoutErr}, wantSource: ntpSourceSystem},
		{name: "trim working custom", configured: " custom.ntp ", configuredResults: []fakeNTPProbeResult{{elapsed: time.Millisecond}}, wantHost: ntpCustom, wantSource: ntpSourceConfigured, saved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, file := ntpService(t, test.configured)
			probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{ntpCustom: test.configuredResults, ntpFast: {test.builtinResult}})
			selection, err := selectAndSaveStartupNTP(context.Background(), service, []string{ntpFast}, probe.probe)
			require.NoError(t, err)
			require.Equal(t, test.wantHost, selection.Host)
			require.Equal(t, test.wantSource, selection.Source)
			require.Equal(t, test.failed, selection.ConfiguredFailed)
			require.Equal(t, test.saved, selection.Saved)
			if test.saved {
				require.Equal(t, 2, file.writes)
			} else {
				require.Equal(t, 1, file.writes)
			}
			var doc manager.Document
			require.NoError(t, json.Unmarshal(file.data, &doc))
			require.Equal(t, test.wantHost, doc.Components[accounttelegram.ID].Values["ntp"])
		})
	}
}

func TestStartupNTPDoesNotOverwriteOnCancellationConflictOrWriteFailure(t *testing.T) {
	for _, cause := range []string{"cancel", "conflict", "write"} {
		t.Run(cause, func(t *testing.T) {
			service, file := ntpService(t, ntpCustom)
			before := append([]byte(nil), file.data...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			expectedErr := errors.New("disk unavailable")
			probe := func(_ context.Context, host string, _ time.Duration) (time.Duration, error) {
				if host == ntpCustom {
					return 0, errors.New("unreachable")
				}
				once.Do(func() {
					switch cause {
					case "cancel":
						cancel()
						expectedErr = context.Canceled
					case "write":
						file.err = expectedErr
					case "conflict":
						catalog, err := application.Catalog()
						require.NoError(t, err)
						require.NoError(t, rte.NewDirectory(catalog, service.Store()).Patch(ctx, accounttelegram.ID, map[string]any{"ntp": "edited.ntp"}))
						before = append([]byte(nil), file.data...)
						expectedErr = rte.ErrConfigurationConflict
					}
				})
				return time.Millisecond, nil
			}
			selection, err := selectAndSaveStartupNTP(ctx, service, []string{ntpFast}, probe)
			require.ErrorIs(t, err, expectedErr)
			require.False(t, selection.Saved)
			require.Equal(t, before, file.data)
		})
	}
}

func TestNTPQueryCancellationInterruptsUDPRead(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := queryNTP(ctx, server.LocalAddr().String(), ntpQueryTimeout); result <- err }()
	require.NoError(t, server.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = server.ReadFrom(make([]byte, 512))
	require.NoError(t, err)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled NTP probe did not stop its UDP read")
	}
}

type ntpTestFile struct {
	data   []byte
	writes int
	err    error
}

func (f *ntpTestFile) Read(ctx context.Context) ([]byte, error) {
	return append([]byte(nil), f.data...), ctx.Err()
}

func (f *ntpTestFile) Write(ctx context.Context, data []byte, _ bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	f.writes++
	f.data = append([]byte(nil), data...)
	return nil
}

func ntpService(t *testing.T, configured string) (*manager.Service, *ntpTestFile) {
	t.Helper()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	file := &ntpTestFile{}
	service := manager.New(file, catalog)
	doc, err := service.Defaults(context.Background())
	require.NoError(t, err)
	doc.Components[accounttelegram.ID].Values["ntp"] = configured
	require.NoError(t, service.Create(context.Background(), doc))
	return service, file
}

func TestSelectStartupNTPPrefersWorkingConfiguredServer(t *testing.T) {
	t.Parallel()

	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		ntpCustom: {{elapsed: 20 * time.Millisecond}},
		ntpFast:   {{elapsed: time.Millisecond}},
	})

	selection := selectStartupNTP(context.Background(), " custom.ntp ", []string{ntpFast}, probe.probe)

	require.Equal(t, ntpCustom, selection.Host)
	require.Equal(t, ntpSourceConfigured, selection.Source)
	require.False(t, selection.ConfiguredFailed)
	require.Equal(t, 1, probe.attempts(ntpCustom))
	require.Zero(t, probe.attempts(ntpFast))
}

func TestSelectStartupNTPFallsBackToFastestBuiltin(t *testing.T) {
	t.Parallel()

	timeoutErr := errors.New("timeout")
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		ntpCustom: {
			{err: timeoutErr},
			{err: timeoutErr},
			{err: timeoutErr},
		},
		ntpSlow:   {{elapsed: 50 * time.Millisecond}},
		ntpFast:   {{elapsed: 5 * time.Millisecond}},
		"bad.ntp": {{err: timeoutErr}},
	})

	selection := selectStartupNTP(context.Background(), ntpCustom, []string{ntpSlow, ntpFast, "bad.ntp"}, probe.probe)

	require.Equal(t, ntpFast, selection.Host)
	require.Equal(t, ntpSourceBuiltin, selection.Source)
	require.True(t, selection.ConfiguredFailed)
	require.Equal(t, 3, probe.attempts(ntpCustom))
	require.Equal(t, 1, probe.attempts(ntpSlow))
	require.Equal(t, 1, probe.attempts(ntpFast))
	require.Equal(t, 1, probe.attempts("bad.ntp"))
}

func TestSelectStartupNTPUsesSystemTimeWhenNoServerWorks(t *testing.T) {
	t.Parallel()

	timeoutErr := errors.New("timeout")
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		ntpCustom: {
			{err: timeoutErr},
			{err: timeoutErr},
			{err: timeoutErr},
		},
		"bad1.ntp": {{err: timeoutErr}},
		"bad2.ntp": {{err: timeoutErr}},
	})

	selection := selectStartupNTP(context.Background(), ntpCustom, []string{"bad1.ntp", "bad2.ntp"}, probe.probe)

	require.Empty(t, selection.Host)
	require.Equal(t, ntpSourceSystem, selection.Source)
	require.True(t, selection.ConfiguredFailed)
	require.Equal(t, 3, probe.attempts(ntpCustom))
}

func TestSelectFastestBuiltinNTPProbesConcurrently(t *testing.T) {
	t.Parallel()

	servers := []string{ntpSlow, ntpFast, "middle.ntp"}
	allStarted := make(chan struct{})
	var closeAllStarted sync.Once
	var mu sync.Mutex
	started := map[string]struct{}{}

	probe := func(ctx context.Context, host string, _ time.Duration) (time.Duration, error) {
		mu.Lock()
		started[host] = struct{}{}
		if len(started) == len(servers) {
			closeAllStarted.Do(func() {
				close(allStarted)
			})
		}
		mu.Unlock()

		select {
		case <-allStarted:
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return 0, errors.New("ntp probes were not concurrent")
		}

		switch host {
		case ntpFast:
			return time.Millisecond, nil
		case "middle.ntp":
			return 10 * time.Millisecond, nil
		default:
			return 50 * time.Millisecond, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	selection := selectFastestBuiltinNTP(ctx, servers, probe)

	require.Equal(t, ntpFast, selection.Host)
	require.Equal(t, ntpSourceBuiltin, selection.Source)
}

type fakeNTPProbeResult struct {
	elapsed time.Duration
	err     error
}

type fakeNTPProbe struct {
	mu      sync.Mutex
	results map[string][]fakeNTPProbeResult
	calls   map[string]int
}

func newFakeNTPProbe(results map[string][]fakeNTPProbeResult) *fakeNTPProbe {
	return &fakeNTPProbe{
		results: results,
		calls:   map[string]int{},
	}
}

func (f *fakeNTPProbe) probe(_ context.Context, host string, _ time.Duration) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	index := f.calls[host]
	f.calls[host]++
	values := f.results[host]
	if index >= len(values) {
		return 0, errors.New("unexpected ntp probe")
	}
	result := values[index]
	return result.elapsed, result.err
}

func (f *fakeNTPProbe) attempts(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[host]
}
