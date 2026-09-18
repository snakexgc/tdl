package forwardtrigger

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/eventbus"
)

const ID = "trigger.forward"

func Register(registry *rte.Registry, handler ports.ForwardIntentHandler, capacity int) error {
	if handler == nil || capacity < 1 {
		return errors.New("forward intents require a handler and positive capacity")
	}
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "转发触发意图",
		Provides:  []manifest.Port{manifest.PortOf[ports.ForwardIntents](ports.ForwardIntentsName, 1, 0)},
		Publishes: []string{types.ForwardRequested}, Subscribes: []string{types.ForwardRequested},
	}, func() rte.Component { return &service{handler: handler, capacity: capacity} })
}

type service struct {
	account  types.AccountID
	events   rte.Events
	handler  ports.ForwardIntentHandler
	capacity int
}

func (s *service) Init(_ context.Context, k rte.Kernel) error {
	s.account, s.events = k.Account, k.Events
	if _, err := k.Events.Subscribe(types.ForwardRequested, s.capacity, func(ctx context.Context, event eventbus.Event) error {
		var request types.ForwardIntent
		if err := json.Unmarshal(event.Payload, &request); err != nil {
			return err
		}
		if request.Account != event.Account {
			return errors.New("forward intent account mismatch")
		}
		return s.handler(ctx, request)
	}, nil); err != nil {
		return err
	}
	return k.Provide(ports.ForwardIntentsName, s)
}

func (s *service) Publish(ctx context.Context, request types.ForwardIntent) error {
	if request.Account != s.account {
		return errors.New("forward intent account mismatch")
	}
	if request.MessageID <= 0 {
		return errors.New("forward intent requires a message ID")
	}
	return s.events.Publish(ctx, types.ForwardRequested, request)
}
func (*service) Start(context.Context) error                    { return nil }
func (*service) Stop(context.Context) error                     { return nil }
func (*service) Reconfigure(context.Context, config.View) error { return nil }
