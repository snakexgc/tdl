package forwarder

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "forwarder"

// Register binds the persistent account queue to its live transport. The queue
// survives connection restarts; each connection gets a fresh single-use runtime.
type Options struct {
	Validate LinkValidator
	Routing  RoutingOptions
}

func Register(registry *rte.Registry, queue *Queue, transport ports.ForwardTransport, completed chan<- error, options ...Options) error {
	if queue == nil || transport == nil {
		return errors.New("forwarder requires a queue and transport")
	}
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	return registry.Register(Manifest(), func() rte.Component {
		return &service{queue: queue, transport: transport, completed: completed, validate: opts.Validate, routing: opts.Routing}
	})
}

type service struct {
	queue     *Queue
	transport ports.ForwardTransport
	runnables *schedule.Group
	completed chan<- error
	validate  LinkValidator
	command   *Command
	routing   RoutingOptions
	router    *MessageRouter
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.runnables = k.Runnables
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	s.router = NewMessageRouter(ctx, k.Account, s.queue, s.routing)
	s.command = NewCommand(ctx, k.Account, s.queue, s.validate, func() CommandSettings { return s.queue.policy().command })
	if err := k.Provide(ports.ForwardRoutingName, s.router); err != nil {
		return err
	}
	if err := k.Provide(commandPort, s.command); err != nil {
		return err
	}
	return k.Provide(ports.ForwardTasksName, s.queue)
}

func (s *service) Start(context.Context) error {
	return s.runnables.Run("forward.queue", 0, 0, func(ctx context.Context) error {
		err := s.queue.Serve(ctx, s.transport)
		if s.completed != nil {
			select {
			case s.completed <- err:
			default:
			}
		}
		return err
	}, nil)
}

func (s *service) Stop(ctx context.Context) error {
	var result error
	if s.command != nil {
		result = s.command.Stop(ctx)
	}
	if s.router != nil {
		result = errors.Join(result, s.router.Stop(ctx))
	}
	return result
}

// LinkValidator is a pure capability supplied by the composition root; the
// forwarder does not import the message-link SWC or its download trigger.
type LinkValidator func(context.Context, types.AccountID, string) (string, error)
