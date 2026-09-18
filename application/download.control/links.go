package downloadcontrol

import (
	"context"
	"errors"
	"strings"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type LinkControl struct {
	account    types.AccountID
	repository ports.DownloadLinkRepository
	local      ports.DownloadBackend
}

func NewLinkControl(account types.AccountID, repository ports.DownloadLinkRepository, local ports.DownloadBackend) *LinkControl {
	return &LinkControl{account, repository, local}
}

func (c *LinkControl) Remove(ctx context.Context, account types.AccountID, id string) (int, error) {
	if account != c.account {
		return 0, errors.New("download link account mismatch")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	id = strings.TrimSpace(id)
	if !validLinkID(id) {
		return 0, errors.New("invalid download link id")
	}
	if c.repository == nil || c.local == nil {
		return 0, errors.New("download link storage is not configured")
	}
	result, err := c.local.ChangeTasks(ctx, actionDelete, []string{id})
	if err != nil {
		return result.Changed, err
	}
	if len(result.Errors) > 0 {
		return result.Changed, errors.New(strings.Join(result.Errors, "; "))
	}
	removed, err := c.repository.Remove(ctx, id)
	return result.Changed + removed, err
}
