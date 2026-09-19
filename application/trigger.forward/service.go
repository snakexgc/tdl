package forwardtrigger

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

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
	return registry.Register(Manifest(), func() rte.Component { return &service{handler: handler, capacity: capacity} })
}

func Manifest() manifest.Manifest {
	return manifest.Manifest{
		ID: ID, Config: []manifest.ConfigField{manifest.List("listen", "Source chats", false), manifest.Flag("listen_comments", "Listen to comments", true, false)}, Title: "转发触发意图",
		Provides:  []manifest.Port{manifest.PortOf[ports.ForwardIntents](ports.ForwardIntentsName, 1, 0), manifest.PortOf[ports.ForwardListening](ports.ForwardListeningName, 1, 0)},
		Publishes: []string{types.ForwardRequested}, Subscribes: []string{types.ForwardRequested},
	}
}

type service struct {
	account  types.AccountID
	events   rte.Events
	handler  ports.ForwardIntentHandler
	capacity int
	ctx      context.Context
	settings atomic.Pointer[types.ForwardListening]
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.ctx = ctx
	s.account, s.events = k.Account, k.Events
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if err := k.Provide(ports.ForwardListeningName, s); err != nil {
		return err
	}
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
func (*service) Start(context.Context) error { return nil }
func (*service) Stop(context.Context) error  { return nil }

func (s *service) Listening(ctx context.Context, account types.AccountID) (types.ForwardListening, error) {
	if err := ctx.Err(); err != nil {
		return types.ForwardListening{}, err
	}
	if err := s.ctx.Err(); err != nil {
		return types.ForwardListening{}, err
	}
	if account != s.account {
		return types.ForwardListening{}, errors.New("forward listening account mismatch")
	}
	settings := *s.settings.Load()
	settings.Sources = append([]string(nil), settings.Sources...)
	return settings, nil
}

func (s *service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var settings types.ForwardListening
	if err := view.Get("listen", &settings.Sources); err != nil {
		return nil, err
	}
	if err := view.Get("listen_comments", &settings.Comments); err != nil {
		return nil, err
	}
	return func() { s.settings.Store(&settings) }, nil
}

func (s *service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}
