package panel

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte"
)

func TestPanelStopCancelsAndDrainsRequestsAndPeriodicSync(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	syncEntered, syncCanceled := make(chan struct{}), make(chan struct{})
	svc := &service{opts: Options{Address: "127.0.0.1:0", Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
		<-release
	}), Sync: func(ctx context.Context) error {
		close(syncEntered)
		<-ctx.Done()
		close(syncCanceled)
		return nil
	}}}
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(manifest.Manifest{ID: ID}, func() rte.Component { return svc }))
	host, err := registry.Build("account", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	defer func() { close(release); require.NoError(t, host.Stop(context.Background())) }()
	client := &http.Client{Timeout: 3 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := client.Get("http://" + svc.listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	for _, signal := range []chan struct{}{entered, syncEntered} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatal("panel did not start")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	for _, signal := range []chan struct{}{canceled, syncCanceled} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatal("panel did not cancel dependent work")
		}
	}
	// The HTTP connection may remain open until its handler has drained.
}
