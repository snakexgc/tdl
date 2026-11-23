package httpdl

import (
	"bytes"
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestForwardedFileNeverStreamsFromUserPool(t *testing.T) {
	ctx := context.Background()
	user := &recordingUploadInvoker{data: []byte("user")}
	proxy := newDownloadProxy(config.HTTPConfig{}, 1, 1, nil, nil, nil)
	proxy.pools.Set(testDownloadPool{client: tg.NewClient(user)})
	task, err := proxy.NewTaskWithPriority(ctx, 42, 17, nil, "test.bin", 4, &tmedia.Media{
		DC: 2, Size: 4, InputFileLoc: &tg.InputDocumentFileLocation{ID: 78, AccessHash: 90},
	}, 0, &types.ForwardedMedia{FileID: "bot-file", BotID: 123})
	require.NoError(t, err)
	lease, err := proxy.scheduler.Acquire(ctx, task.ID, task.Media.DC)
	require.NoError(t, err)
	defer lease.Release()
	var out bytes.Buffer
	require.Error(t, proxy.Stream(ctx, task, lease, 0, 3, &out))
	require.Zero(t, user.totalCalls())
}
