package login

import (
	"context"

	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/tclient"
)

type DialogTransport struct{ Options func() SessionOptions }

func (p DialogTransport) Dialogs(ctx context.Context) ([]types.Dialog, error) {
	opts := p.Options()
	c, err := tclient.New(ctx, tclient.Options{
		Connections: opts.Connections, Credentials: opts.Credentials,
		Account: opts.Account, KV: opts.KV, Proxy: opts.Proxy, ReconnectTimeout: opts.ReconnectTimeout,
	}, false)
	if err != nil {
		return nil, err
	}
	var result []types.Dialog
	err = c.Run(ctx, func(ctx context.Context) error {
		status, err := c.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			return ErrSessionUnauthorized
		}
		result, err = tgauth.Dialogs(ctx, c.API(), opts.KV)
		return err
	})
	return result, err
}
