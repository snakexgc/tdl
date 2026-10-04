package httpdl

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

type downloadTestClock struct{ at time.Time }

func (c downloadTestClock) Now() time.Time { return c.at }
func (downloadTestClock) Status() types.ClockStatus {
	return types.ClockStatus{Source: types.ClockSourceNTP, Synchronized: true}
}

func TestHTTPRequestsUseTheSharedClockForPersistentActivity(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	first := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	binding.Bind(downloadTestClock{first})
	cfg := config.DefaultConfig()
	cfg.HTTP.Address, cfg.HTTP.Port = testHTTPAddress, reserveTCPPort(t)
	store := newMemoryTaskStorage()
	service := NewService(cfg, store, nil)
	service.Proxy().Tasks().SetTTL(time.Hour)
	task, err := service.Proxy().NewTask(ctx, 1, 2, nil, testFileName, 10, &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{ID: 123}, Name: testFileName, Size: 10, DC: 2,
	})
	require.NoError(t, err)
	require.Equal(t, first, task.CreatedAt)
	controller := NewController(ctx, service)
	t.Cleanup(controller.Stop)
	require.True(t, controller.Start())
	second := first.Add(20 * time.Minute)
	binding.Bind(downloadTestClock{second})
	client := &http.Client{Timeout: time.Second}
	request, err := http.NewRequest(http.MethodHead, "http://"+config.HTTPListenAddr(cfg)+"/download/"+task.ID, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	// Read the raw record so a second lookup cannot refresh it and mask a wrong
	// clock on the HTTP request's context.
	data, err := taskhub.Links(store).Get(ctx, task.ID)
	require.NoError(t, err)
	var persisted struct {
		LastActiveAt time.Time `json:"last_active_at"`
	}
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Equal(t, second, persisted.LastActiveAt)
}
