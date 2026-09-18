package rte

import (
	"context"
	"fmt"
	"slices"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/eventbus"
)

// Events is the account- and manifest-scoped event capability of a component.
// Requests that require a reply use typed ports, not a second RPC mechanism.
type Events struct {
	bus        *eventbus.Bus
	account    types.AccountID
	ctx        context.Context
	publishes  []string
	subscribes []string
	report     eventbus.Reporter
}

func (e Events) Publish(ctx context.Context, topic string, payload any) error {
	if !slices.Contains(e.publishes, topic) {
		return fmt.Errorf("undeclared published topic %s", topic)
	}
	if err := e.ctx.Err(); err != nil {
		return err
	}
	return e.bus.Publish(ctx, e.account, topic, payload)
}

func (e Events) Subscribe(topic string, capacity int, handler eventbus.Handler, report eventbus.Reporter) (func(), error) {
	if !slices.Contains(e.subscribes, topic) {
		return nil, fmt.Errorf("undeclared subscribed topic %s", topic)
	}
	return e.bus.Subscribe(e.ctx, e.account, topic, capacity, handler, func(topic string, err error) {
		if e.report != nil {
			e.report(topic, err)
		}
		if report != nil {
			report(topic, err)
		}
	})
}
