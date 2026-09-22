package httpdl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
)

// GetLink validates and restores Telegram media without exposing SDK objects
// to the local queue component.
func (s *taskStore) GetLink(ctx context.Context, id string) (ports.ObservedLink, bool, error) {
	if s == nil || s.kv == nil {
		return ports.ObservedLink{}, false, fmt.Errorf("saved download storage is unavailable")
	}
	data, err := taskhub.Links(s.kv).Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return ports.ObservedLink{}, false, nil
	}
	if err != nil {
		return ports.ObservedLink{}, false, err
	}
	var saved persistentDownloadTask
	if err := json.Unmarshal(data, &saved); err != nil {
		return ports.ObservedLink{}, false, err
	}
	if saved.ID != id {
		return ports.ObservedLink{}, false, fmt.Errorf("download source identity mismatch")
	}
	task, err := saved.ToTask()
	if err != nil {
		return ports.ObservedLink{}, false, fmt.Errorf("restore persistent download task: %w", err)
	}
	peerID := task.PeerID
	if peerID == 0 && task.Peer != nil {
		peerID = tutil.GetInputPeerID(task.Peer)
	}
	return ports.ObservedLink{Task: types.PersistentLink{ID: task.ID, PeerID: peerID, MessageID: task.MessageID, FileName: task.FileName, FileSize: task.FileSize, CreatedAt: task.CreatedAt, LastActiveAt: task.LastActiveAt}, Version: taskhub.LinkVersion(data)}, true, nil
}
