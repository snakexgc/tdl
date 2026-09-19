package taskhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const fieldControlUntil = "control_until"

// ReserveControl creates a bounded fence around network I/O, without holding
// a transaction open. Expiry permits recovery after a crashed caller.
func (s *Aria2Repository) ReserveControl(ctx context.Context, expected types.Aria2TaskRecord, until time.Time) (types.Aria2TaskRecord, bool, error) {
	var lease types.Aria2TaskRecord
	reserved := false
	if !until.After(time.Now()) {
		return lease, false, fmt.Errorf("control deadline has expired")
	}
	err := Aria2(s.kv).Mutate(ctx, expected.GID, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		if err := json.Unmarshal(data, &lease); err != nil {
			return nil, stamp, err
		}
		if lease.Deleted || lease.Revision != expected.Revision || lease.TaskID != expected.TaskID || lease.ControlUntil.After(time.Now()) {
			return data, stamp, nil
		}
		lease.GID, lease.Revision, lease.ControlUntil = expected.GID, lease.Revision+1, until
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, stamp, err
		}
		raw[fieldRevision], _ = json.Marshal(lease.Revision)
		raw[fieldControlUntil], _ = json.Marshal(until)
		next, err := json.Marshal(raw)
		reserved = err == nil
		return next, time.Now(), err
	})
	if errors.Is(err, storage.ErrNotFound) {
		return lease, false, nil
	}
	return lease, reserved && err == nil, err
}

// FinishControl only updates the lease that performed the RPC. Both successful
// and failed calls advance the version so observations started during I/O are
// rejected. A missing/replaced record is never recreated.
func (s *Aria2Repository) FinishControl(ctx context.Context, lease types.Aria2TaskRecord, status string, remove bool) (bool, error) {
	changed := false
	err := Aria2(s.kv).Mutate(ctx, lease.GID, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		var current types.Aria2TaskRecord
		if err := json.Unmarshal(data, &current); err != nil {
			return nil, stamp, err
		}
		if current.Revision != lease.Revision || current.TaskID != lease.TaskID {
			return data, stamp, nil
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, stamp, err
		}
		delete(raw, fieldControlUntil)
		if remove {
			// Retain a tombstone until normal retention expires. An observer
			// started just after deletion can still receive a stale active RPC.
			raw["deleted"] = json.RawMessage("true")
			status = string(types.DownloadRemoved)
		}
		if status != "" && types.DownloadTransition(types.NormalizeDownloadState(current.Status), types.NormalizeDownloadState(status)) {
			raw[fieldStatus], _ = json.Marshal(status)
			raw[fieldState], _ = json.Marshal(types.NormalizeDownloadState(status))
			delete(raw, "pause_owner")
			if status == string(types.DownloadPaused) && lease.PauseOwner != "" {
				raw["pause_owner"], _ = json.Marshal(lease.PauseOwner)
			}
		}
		raw[fieldRevision], _ = json.Marshal(current.Revision + 1)
		next, err := json.Marshal(raw)
		changed = err == nil
		return next, time.Now(), err
	})
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return changed && err == nil, err
}
