package listen

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestJournalReopensBoltAndNamespacesStayIndependent(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverFile, filepath.Join(t.TempDir(), "journal"))
	require.NoError(t, err)
	defer func() { require.NoError(t, engine.Close()) }()
	account, err := engine.Open("default")
	require.NoError(t, err)
	repository := New(account)
	peer := types.MessagePeer{Kind: "user", ID: 1}
	job := types.ListenJobRecord{Version: 1, ID: "event", Message: types.ListenMessage{Peer: peer, ID: 2, Date: time.Now().Unix(), MediaID: "document_3"}, State: "pending"}
	require.NoError(t, repository.Update(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}}))
	state, err := New(account).Load(ctx)
	require.NoError(t, err)
	require.Equal(t, job, state.Jobs["event"])
	other, err := engine.Open("other")
	require.NoError(t, err)
	isolated, err := New(other).Load(ctx)
	require.NoError(t, err)
	require.Empty(t, isolated.Jobs)
}

func TestJournalRejectsUnsupportedVersionsWithoutChangingRecords(t *testing.T) {
	ctx := context.Background()
	store := &storage.Memory{}
	raw, err := json.Marshal(metadata{Version: 99, Sources: map[types.ChatRef]types.ListenSourceRecord{}})
	require.NoError(t, err)
	require.NoError(t, store.Set(ctx, prefix+"state", raw))
	_, err = New(store).Load(ctx)
	require.Error(t, err)
	require.Error(t, New(store).Update(ctx, types.ListenMutation{}))
	after, err := store.Get(ctx, prefix+"state")
	require.NoError(t, err)
	require.Equal(t, raw, after)
}
