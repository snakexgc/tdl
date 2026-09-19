package taskhub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

func mergeAria2State(previous, updates map[string]json.RawMessage) error {
	var oldStatus, status string
	var revision uint64
	_ = json.Unmarshal(previous[fieldStatus], &oldStatus)
	_ = json.Unmarshal(previous[fieldRevision], &revision)
	_ = json.Unmarshal(updates[fieldStatus], &status)
	if status == "" {
		// AddURI bookkeeping refreshes metadata, not an observed remote state.
		for _, field := range []string{fieldStatus, fieldState, fieldTotal, fieldCompleted, fieldError} {
			delete(updates, field)
		}
		status = oldStatus
		if status == "" {
			status = "waiting"
			updates[fieldStatus], _ = json.Marshal(status)
		}
	}
	next := types.NormalizeDownloadState(status)
	if !types.DownloadTransition(types.NormalizeDownloadState(oldStatus), next) {
		return fmt.Errorf("invalid aria2 state transition %q to %q", oldStatus, status)
	}
	updates[fieldState], _ = json.Marshal(next)
	updates[fieldRevision], _ = json.Marshal(revision + 1)
	return nil
}

// Report applies a remote observation only to the revision captured before the
// network request. It never creates a removed record or replaces its metadata.
func (s *Aria2Repository) Report(ctx context.Context, observation types.Aria2TaskRecord, revision uint64) (bool, error) {
	changed := false
	err := Aria2(s.kv).Mutate(ctx, observation.GID, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		var current types.Aria2TaskRecord
		if err := json.Unmarshal(data, &current); err != nil {
			return nil, stamp, err
		}
		if current.Deleted || current.TaskID != observation.TaskID || current.Revision != revision || current.ControlUntil.After(time.Now()) {
			return data, stamp, nil
		}
		if !types.DownloadTransition(types.NormalizeDownloadState(current.Status), types.NormalizeDownloadState(observation.Status)) {
			return data, stamp, nil
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, stamp, err
		}
		if raw == nil {
			raw = map[string]json.RawMessage{}
		}
		for field, value := range map[string]any{
			fieldStatus: observation.Status, fieldState: types.NormalizeDownloadState(observation.Status),
			fieldTotal: observation.Total, fieldCompleted: observation.Completed, fieldError: observation.Error, fieldRevision: revision + 1,
		} {
			raw[field], _ = json.Marshal(value)
		}
		if types.NormalizeDownloadState(observation.Status) != types.DownloadComplete {
			stamp = time.Now()
		}
		next, err := json.Marshal(raw)
		changed = err == nil
		return next, stamp, err
	})
	return changed && err == nil, err
}

const fieldCompleted = "completed"

const fieldError = "error"

const (
	fieldStatus   = "status"
	fieldState    = "state"
	fieldTotal    = "total"
	fieldRevision = "revision"
)
