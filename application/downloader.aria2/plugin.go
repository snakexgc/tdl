package aria2

import (
	"context"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "downloader.aria2"

func Register(registry *rte.Registry, manager *Manager, finished chan<- error) error {
	if manager == nil || manager.client == nil || manager.store == nil {
		return errors.New("aria2 requires a client and repository")
	}
	return registry.Register(Manifest(), func() rte.Component { return &service{manager: manager, finished: finished} })
}

type service struct {
	manager   *Manager
	runnables *schedule.Group
	finished  chan<- error
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.runnables = k.Runnables
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if err := k.Provide(ports.Aria2TasksName, s.manager.controller); err != nil {
		return err
	}
	return k.Provide(ports.DownloadExecutorName, s.manager)
}

func (s *service) Start(context.Context) error {
	if err := s.runnables.RunDynamic("aria2.status", s.manager.policy().status, func() time.Duration { return s.manager.policy().status }, s.manager.statusChanged, s.manager.syncStates, nil); err != nil {
		return err
	}
	return s.runnables.Run("aria2.manager", 0, 0, func(ctx context.Context) error {
		err := s.manager.Run(ctx)
		if s.finished != nil {
			select {
			case s.finished <- err:
			default:
			}
		}
		return err
	}, nil)
}
func (*service) Stop(context.Context) error { return nil }
