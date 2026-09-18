package aria2

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	gferrors "github.com/go-faster/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWaitForAria2RetriesConnectionErrors(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{
			fakeAria2ConnectionError(),
			fakeAria2ConnectionError(),
		},
	}

	err := RetryConnectionWithInterval(context.Background(), zap.NewNop(), "set aria2 max concurrent downloads", time.Millisecond, func() error {
		return client.SetMaxConcurrentDownloads(context.Background(), 3)
	})
	require.NoError(t, err)
	require.Equal(t, 3, client.calls)
	require.Equal(t, []int{3, 3, 3}, client.limits)
}

func TestWaitForAria2DoesNotRetryRPCError(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{gferrors.New("aria2 rpc error 1: unauthorized")},
	}

	err := RetryConnectionWithInterval(context.Background(), zap.NewNop(), "set aria2 max concurrent downloads", time.Millisecond, func() error {
		return client.SetMaxConcurrentDownloads(context.Background(), 3)
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "unauthorized")
	require.Equal(t, 1, client.calls)
}

type fakeAria2ConcurrentDownloadSetter struct {
	calls  int
	limits []int
	errs   []error
}

func (f *fakeAria2ConcurrentDownloadSetter) SetMaxConcurrentDownloads(ctx context.Context, limit int) error {
	f.calls++
	f.limits = append(f.limits, limit)
	if len(f.errs) == 0 {
		return nil
	}

	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func fakeAria2ConnectionError() error {
	return gferrors.Wrap(&url.Error{
		Op:  "Post",
		URL: "http://127.0.0.1:6800/jsonrpc",
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: gferrors.New("connection refused"),
		},
	}, "do aria2 request")
}
