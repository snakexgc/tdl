package aria2

import (
	"context"
	"strings"

	"github.com/go-faster/errors"
)

// SyncStates persists only records belonging to this account. The repository
// compares the revision read before RPC, so concurrent controls/reporters win.
func (c *Controller) SyncStates(ctx context.Context) error {
	statuses, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, status := range statuses {
		record, exists := records[status.GID]
		if !exists {
			continue
		}
		revision := record.Revision
		record.Status = status.Status
		info := aria2TaskInfo(status)
		record.Total, record.Completed = info.TotalLength, info.CompletedLength
		record.Error = strings.TrimSpace(status.ErrorCode + " " + status.ErrorMessage)
		_, err := c.store.Report(ctx, record, revision)
		result = errors.Join(result, err)
	}
	return result
}
