package timesync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type networkProxyStub struct {
	url   string
	err   error
	calls atomic.Int32
}

func (p *networkProxyStub) Proxy(context.Context) (string, error) {
	p.calls.Add(1)
	return p.url, p.err
}

type proxyProbeStub struct {
	probeFunc
	viaProxy func(context.Context, string, string, time.Duration) (types.TimeSample, error)
}

func (p proxyProbeStub) QueryViaProxy(ctx context.Context, host, proxy string, timeout time.Duration) (types.TimeSample, error) {
	return p.viaProxy(ctx, host, proxy, timeout)
}

func TestProxyRetryStartsAfterDirectRoundIsDrained(t *testing.T) {
	proxy := &networkProxyStub{url: "socks5://user:secret@proxy.test:1080"}
	var active, directCalls, proxyCalls atomic.Int32
	probe := proxyProbeStub{
		probeFunc: func(context.Context, string, time.Duration) (types.TimeSample, error) {
			active.Add(1)
			defer active.Add(-1)
			directCalls.Add(1)
			return types.TimeSample{}, errors.New("offline")
		},
		viaProxy: func(ctx context.Context, host, url string, _ time.Duration) (types.TimeSample, error) {
			proxyCalls.Add(1)
			if active.Load() != 0 || url != proxy.url {
				return types.TimeSample{}, errors.New("incorrect retry ordering or proxy")
			}
			if host == sampleServer {
				return types.TimeSample{Offset: 2 * time.Minute, MeasuredAt: time.Now()}, nil
			}
			<-ctx.Done()
			return types.TimeSample{}, ctx.Err()
		},
	}
	s := New(probe)
	s.proxy = proxy
	configure(t, s, nil)
	require.NoError(t, s.synchronize(context.Background()))
	require.EqualValues(t, len(builtinServers), directCalls.Load())
	require.EqualValues(t, len(builtinServers), proxyCalls.Load(), "exactly one concurrent proxy round")
	require.EqualValues(t, 1, proxy.calls.Load())
	require.Equal(t, types.ClockSourceNTP, s.Status().Source)
	require.True(t, s.Status().ViaProxy)
	require.Equal(t, sampleServer, s.Status().Server)
	require.WithinDuration(t, time.Now().Add(2*time.Minute), s.Now(), time.Second)
}

func TestDirectSuccessDoesNotAccessProxy(t *testing.T) {
	proxy := &networkProxyStub{url: "http://proxy.test:8080"}
	s := New(proxyProbeStub{
		probeFunc: func(context.Context, string, time.Duration) (types.TimeSample, error) {
			return types.TimeSample{Offset: time.Minute}, nil
		},
		viaProxy: func(context.Context, string, string, time.Duration) (types.TimeSample, error) {
			t.Error("unexpected proxy retry")
			return types.TimeSample{}, errors.New("unexpected proxy retry")
		},
	})
	s.proxy = proxy
	configure(t, s, nil)
	require.NoError(t, s.synchronize(context.Background()))
	require.Zero(t, proxy.calls.Load())
	require.False(t, s.Status().ViaProxy)
}

func TestFailedProxyRetryUsesSystemTimeAndRedactsErrors(t *testing.T) {
	const secretURL = "socks5://user:private-password@proxy.test:1080"
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{true: "provider_failure", false: "query_failure"}[unavailable], func(t *testing.T) {
			proxy := &networkProxyStub{url: secretURL}
			if unavailable {
				proxy.err = errors.New(secretURL)
			}
			var proxyCalls atomic.Int32
			s := New(proxyProbeStub{
				probeFunc: func(context.Context, string, time.Duration) (types.TimeSample, error) {
					return types.TimeSample{}, errors.New("offline")
				},
				viaProxy: func(context.Context, string, string, time.Duration) (types.TimeSample, error) {
					proxyCalls.Add(1)
					return types.TimeSample{}, errors.New(secretURL)
				},
			})
			s.proxy = proxy
			configure(t, s, nil)
			err := s.synchronize(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-password")
			require.NotContains(t, s.Status().LastError, secretURL)
			require.Equal(t, types.ClockSourceSystem, s.Status().Source)
			require.False(t, s.Status().Synchronized)
			require.False(t, s.Status().ViaProxy)
			require.WithinDuration(t, time.Now(), s.Now(), time.Second)
			if unavailable {
				require.Zero(t, proxyCalls.Load())
			} else {
				require.EqualValues(t, len(builtinServers), proxyCalls.Load())
			}
		})
	}
}

func TestHTTPSProxySamplePublishesActualSource(t *testing.T) {
	s := New(proxyProbeStub{
		probeFunc: func(context.Context, string, time.Duration) (types.TimeSample, error) {
			return types.TimeSample{}, errors.New("offline")
		},
		viaProxy: func(context.Context, string, string, time.Duration) (types.TimeSample, error) {
			return types.TimeSample{Offset: time.Minute, Source: types.ClockSourceHTTPS, Server: "https://time.test/", MeasuredAt: time.Now()}, nil
		},
	})
	s.proxy = &networkProxyStub{url: "http://proxy.test:8080"}
	configure(t, s, nil)
	require.NoError(t, s.synchronize(context.Background()))
	require.Equal(t, "https://time.test/", s.Status().Server)
	require.Equal(t, types.ClockSourceHTTPS, s.Status().Source)
	require.True(t, s.Status().ViaProxy)
}
