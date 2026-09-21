// Package ntp adapts cancellable UDP clock measurements to the time service.
package ntp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/beevik/ntp"

	"github.com/snakexgc/tdl/interfaces/types"
)

type Client struct{}

func (Client) Query(ctx context.Context, host string, timeout time.Duration) (types.TimeSample, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stopCancel func() bool
	defer func() {
		if stopCancel != nil {
			stopCancel()
		}
	}()
	started := time.Now()
	response, err := ntp.QueryWithOptions(host, ntp.QueryOptions{
		Timeout: timeout,
		Dialer: func(_, remote string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(queryCtx, "udp", remote)
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
	return types.TimeSample{Offset: response.ClockOffset, Elapsed: time.Since(started)}, nil
}
