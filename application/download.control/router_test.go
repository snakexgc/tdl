package downloadcontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type routingExecutor struct {
	calls int
	err   error
	id    string
}

func (*routingExecutor) Name() string { return "test" }
func (e *routingExecutor) Submit(context.Context, types.DownloadSubmission) (types.DownloadResult, error) {
	e.calls++
	return types.DownloadResult{ID: e.id}, e.err
}

func TestRoutingOnlyFallsBackBeforeAcceptance(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		id       string
		fallback bool
	}{
		{"rejected", ports.ErrDownloadNotAccepted, "", true},
		{"ambiguous", errors.New("connection reset after request"), "", false},
		{"accepted", ports.ErrDownloadNotAccepted, "remote-id", false},
		{"successful", nil, "remote-id", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary, fallback := &routingExecutor{err: test.err, id: test.id}, &routingExecutor{id: "fallback-id"}
			router := NewRouter("alice", primary, fallback)
			result, err := router.Submit(context.Background(), types.DownloadSubmission{Account: "alice"})
			require.Equal(t, 1, primary.calls)
			if test.fallback {
				require.NoError(t, err)
				require.Equal(t, "fallback-id", result.ID)
				require.Equal(t, 1, fallback.calls)
			} else {
				require.Equal(t, 0, fallback.calls)
				require.ErrorIs(t, err, test.err)
			}
		})
	}
}

func TestRoutingRejectsWrongAccountAndCancellation(t *testing.T) {
	executor := &routingExecutor{}
	router := NewRouter("alice", executor)
	_, err := router.Submit(context.Background(), types.DownloadSubmission{Account: "bob"})
	require.ErrorContains(t, err, "account mismatch")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = router.Submit(ctx, types.DownloadSubmission{Account: "alice"})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, executor.calls)
}
