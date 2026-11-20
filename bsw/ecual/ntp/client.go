// Package ntp adapts cancellable UDP clock measurements to the time service.
package ntp

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/beevik/ntp"

	"github.com/snakexgc/tdl/interfaces/types"
)

type Client struct{}

const (
	proxySchemeHTTP   = "http"
	proxySchemeHTTPS  = "https"
	proxySchemeSOCKS5 = "socks5"
)

func (Client) Query(ctx context.Context, host string, timeout time.Duration) (types.TimeSample, error) {
	return query(ctx, host, timeout, func(ctx context.Context, remote string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", remote)
	})
}

func (Client) QueryViaProxy(ctx context.Context, host, proxyURL string, timeout time.Duration) (types.TimeSample, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Hostname() == "" {
		return types.TimeSample{}, fmt.Errorf("invalid time proxy")
	}
	switch parsed.Scheme {
	case proxySchemeSOCKS5:
		sample, err := query(queryCtx, host, timeout, func(ctx context.Context, remote string) (net.Conn, error) {
			return dialSOCKSUDP(ctx, parsed, remote)
		})
		if err == nil || queryCtx.Err() != nil {
			return sample, err
		}
		// TCP-only SOCKS5 servers can still supply time over authenticated HTTPS.
		return queryHTTPS(queryCtx, host, proxyURL)
	case proxySchemeHTTP, proxySchemeHTTPS:
		return queryHTTPS(queryCtx, host, proxyURL)
	default:
		return types.TimeSample{}, fmt.Errorf("unsupported time proxy protocol")
	}
}

func query(ctx context.Context, host string, timeout time.Duration, dial func(context.Context, string) (net.Conn, error)) (types.TimeSample, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stopCancel func() bool
	defer func() {
		if stopCancel != nil {
			stopCancel()
		}
	}()
	started := time.Now()
	var measuredAt time.Time
	response, err := ntp.QueryWithOptions(host, ntp.QueryOptions{
		Timeout: timeout,
		GetSystemTime: func() time.Time {
			// Keep both NTP timestamps on one monotonic timeline if OS time steps
			// while the datagram is in flight. The final callback is receive time.
			measuredAt = started.Add(time.Since(started))
			return measuredAt
		},
		Dialer: func(_, remote string) (net.Conn, error) {
			conn, err := dial(queryCtx, remote)
			if err != nil {
				return nil, err
			}
			// Cancellation interrupts DNS/dial and an already pending UDP read.
			stopCancel = context.AfterFunc(queryCtx, func() { _ = conn.Close() })
			return conn, nil
		},
	})
	if queryCtx.Err() != nil {
		return types.TimeSample{}, queryCtx.Err()
	}
	if err != nil {
		return types.TimeSample{}, err
	}
	if response == nil {
		return types.TimeSample{}, fmt.Errorf("empty NTP response")
	}
	if err := response.Validate(); err != nil {
		return types.TimeSample{}, err
	}
	return types.TimeSample{Offset: response.ClockOffset, Elapsed: time.Since(started), MeasuredAt: measuredAt, Source: types.ClockSourceNTP, Server: host}, nil
}
