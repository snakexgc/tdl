package httpdl

import (
	"context"
	"testing"

	"github.com/gotd/td/fileid"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestBotFileReferencePersistsAndRefreshesWithoutMessagePeer(t *testing.T) {
	oldID, err := fileid.EncodeFileID(fileid.FileID{Type: fileid.Document, DC: 2, ID: 78, AccessHash: 90, FileReference: []byte("old")})
	require.NoError(t, err)
	newID, err := fileid.EncodeFileID(fileid.FileID{Type: fileid.Document, DC: 2, ID: 78, AccessHash: 90, FileReference: []byte("new")})
	require.NoError(t, err)
	proxy := newDownloadProxy(config.HTTPConfig{}, 1, 1, nil, nil, nil)
	media := &tmedia.Media{InputFileLoc: &tg.InputDocumentFileLocation{ID: 78, AccessHash: 90, FileReference: []byte("old")}, Name: "file.bin", Size: 12, DC: 2}
	task, err := proxy.NewTask(context.Background(), 42, 17, nil, "file.bin", 12, media, oldID)
	require.NoError(t, err)
	persisted, err := persistentDownloadTaskFromTask(task)
	require.NoError(t, err)
	restored, err := persisted.ToTask()
	require.NoError(t, err)
	require.Equal(t, oldID, restored.BotFileID)
	require.Nil(t, restored.Peer)

	proxy.SetBotFileRefresher(func(_ context.Context, fileID string) (string, error) {
		require.Equal(t, oldID, fileID)
		return newID, nil
	})
	require.NoError(t, proxy.refreshTaskMedia(context.Background(), restored))
	updated, ok, err := proxy.Tasks().Get(context.Background(), task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, newID, updated.BotFileID)
	require.Equal(t, []byte("new"), updated.Media.InputFileLoc.(*tg.InputDocumentFileLocation).FileReference)
	wrongID, err := fileid.EncodeFileID(fileid.FileID{Type: fileid.Document, DC: 2, ID: 79, AccessHash: 90, FileReference: []byte("other")})
	require.NoError(t, err)
	proxy.SetBotFileRefresher(func(context.Context, string) (string, error) { return wrongID, nil })
	require.ErrorContains(t, proxy.refreshTaskMedia(context.Background(), updated), "changed media identity")

	proxy.SetBotFileRefresher(nil)
	require.ErrorContains(t, proxy.refreshTaskMedia(context.Background(), updated), "unavailable")
}
