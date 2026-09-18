package forwarder

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "forwarder"

// Register binds the persistent account queue to its live transport. The queue
// survives connection restarts; each connection gets a fresh single-use runtime.
func Register(registry *rte.Registry, queue *Queue, transport ports.ForwardTransport, completed chan<- error) error {
	if queue == nil || transport == nil {
		return errors.New("forwarder requires a queue and transport")
	}
	return registry.Register(Manifest(), func() rte.Component { return &service{queue: queue, transport: transport, completed: completed} })
}

type service struct {
	queue     *Queue
	transport ports.ForwardTransport
	runnables *schedule.Group
	completed chan<- error
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.runnables = k.Runnables
	if err := s.Reconfigure(ctx, k.Config); err != nil {
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
func (*service) Stop(context.Context) error { return nil }
