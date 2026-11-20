package httpdl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tmedia"
)

func TestTaskStoreSelectsWholeDownloadSource(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	makeSource := func(peerID int64, messageID int, botID int64) downloadTask {
		task := downloadTask{
			PeerID: peerID, MessageID: messageID,
			FileName: "media.bin", FileSize: 12, LastActiveAt: now, CreatedAt: now,
			Media: &tmedia.Media{Size: 12, DC: 2, InputFileLoc: &tg.InputDocumentFileLocation{ID: 42, AccessHash: peerID}},
		}
		task.ID = tmedia.Identity(task.Media)
		if botID == 0 {
			task.Peer = &tg.InputPeerChat{ChatID: peerID}
		} else {
			task.BotFileID, task.BotID = "bot-file", botID
		}
		return task
	}
	user := makeSource(123, 1, 0)
	bot := makeSource(234, 2, 9)
	otherBot := makeSource(345, 3, 10)
	for _, tc := range []struct {
		name                string
		first, second, want downloadTask
	}{
		{"user then bot", user, bot, user},
		{"bot then user", bot, user, user},
		{"replacement bot", bot, otherBot, otherBot},
	} {
		for _, persistent := range []bool{false, true} {
			name := tc.name + "/memory"
			var kv storage.Storage
			if persistent {
				name = tc.name + "/persistent"
				kv = newMemoryTaskStorage()
			}
			t.Run(name, func(t *testing.T) {
				store := newTaskStore(kv, 0)
				first, second := tc.first, tc.second
				first.Priority = 2
				second.LastActiveAt = now.Add(time.Minute)
				if persistent && first.Peer != nil {
					// A link saved by master predates both bot identity fields.
					data, err := persistentDownloadTaskFromTask(&first)
					require.NoError(t, err)
					raw, err := json.Marshal(data)
					require.NoError(t, err)
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(raw, &fields))
					delete(fields, "bot_file_id")
					delete(fields, "bot_id")
					raw, err = json.Marshal(fields)
					require.NoError(t, err)
					require.NoError(t, taskhub.Links(kv).Put(ctx, first.ID, raw, first.LastActiveAt))
				} else {
					require.NoError(t, store.Add(ctx, &first))
				}
				require.NoError(t, store.Add(ctx, &second))
				if persistent {
					store = newTaskStore(kv, 0)
				}
				got, found, err := store.Get(ctx, first.ID)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, tc.want.Peer, got.Peer)
				require.Equal(t, tc.want.PeerID, got.PeerID)
				require.Equal(t, tc.want.MessageID, got.MessageID)
				require.Equal(t, tc.want.Media, got.Media)
				require.Equal(t, tc.want.BotID, got.BotID)
				require.Equal(t, tc.want.BotFileID, got.BotFileID)
				require.Equal(t, 2, got.Priority)
				require.True(t, got.LastActiveAt.Equal(second.LastActiveAt))
			})
		}
	}
}
