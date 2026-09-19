package accounttelegram

import (
	"context"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type Dialogs struct {
	mu        sync.Mutex
	transport ports.DialogCatalog
	items     []types.Dialog
	at        time.Time
}

func NewDialogs(transport ports.DialogCatalog) *Dialogs { return &Dialogs{transport: transport} }

func (d *Dialogs) Dialogs(ctx context.Context) ([]types.Dialog, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if time.Since(d.at) > 30*time.Second {
		items, err := d.transport.Dialogs(ctx)
		if err != nil {
			return nil, err
		}
		d.items, d.at = items, time.Now()
	}
	return append([]types.Dialog{}, d.items...), nil
}
