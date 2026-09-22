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
	refresh   chan struct{}
}

func NewDialogs(transport ports.DialogCatalog) *Dialogs { return &Dialogs{transport: transport} }

func (d *Dialogs) Dialogs(ctx context.Context) ([]types.Dialog, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d.mu.Lock()
		if time.Since(d.at) <= 30*time.Second {
			items := append([]types.Dialog{}, d.items...)
			d.mu.Unlock()
			return items, nil
		}
		if pending := d.refresh; pending != nil {
			d.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		d.refresh = make(chan struct{})
		d.mu.Unlock()
		return d.load(ctx)
	}
}

func (d *Dialogs) load(ctx context.Context) ([]types.Dialog, error) {
	// Release waiters even if the request boundary recovers a transport panic.
	defer func() {
		d.mu.Lock()
		close(d.refresh)
		d.refresh = nil
		d.mu.Unlock()
	}()
	// The network request owns no shared lock. Other callers may cancel
	// independently while still sharing a successful refresh.
	items, err := d.transport.Dialogs(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.items, d.at = append([]types.Dialog{}, items...), time.Now()
	d.mu.Unlock()
	return append([]types.Dialog{}, items...), nil
}
