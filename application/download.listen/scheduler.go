package downloadlisten

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type work struct {
	key    string
	kind   string
	source types.ListenSourceRecord
	thread types.ListenThreadRecord
	job    types.ListenJobRecord
}

// Two workers share one due queue. There is no timer/goroutine per post.
func (s *Service) Serve(ctx context.Context, resources ports.ListenResources) error {
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	if resources.Transport == nil || resources.Downloads == nil || resources.Enabled == nil {
		return fmt.Errorf("监听连接资源不完整")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return context.Canceled
	}
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	s.serving.Add(1)
	s.connected, s.enabled = true, resources.Enabled
	for ref := range s.current.record.Starts {
		source := s.sourceLocked(ref)
		source.NextScanAt = time.Time{}
		s.state.Sources[ref] = source
	}
	s.mu.Unlock()
	defer func() {
		unlink()
		cancel()
		s.mu.Lock()
		s.connected = false
		s.active = map[string]bool{}
		s.mu.Unlock()
		s.serving.Done()
	}()
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for call.Err() == nil {
				item, found := s.nextWork(resources.Enabled())
				if !found {
					select {
					case <-call.Done():
						return
					case <-s.wake:
					case <-ticker.C:
					}
					continue
				}
				err := s.perform(call, item, resources)
				s.completeWork(call, item, err)
			}
		}()
	}
	workers.Wait()
	return call.Err()
}

func (s *Service) nextWork(enabled bool) (work, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if !enabled || s.closed || now.Before(s.blockedUntil) {
		return work{}, false
	}
	var selected work
	due := now.Add(time.Nanosecond)
	choose := func(item work, at time.Time) {
		if s.active[item.key] || at.After(now) {
			return
		}
		if at.Before(due) || (at.Equal(due) && item.key < selected.key) {
			due, selected = at, item
		}
	}
	for ref := range s.current.record.Starts {
		source := s.sourceLocked(ref)
		choose(work{key: "source/" + string(ref), kind: workSource, source: source}, source.NextScanAt)
	}
	for _, thread := range s.state.Threads {
		if !now.Before(thread.ExpiresAt) {
			choose(work{key: "thread/" + thread.ID, kind: workThread, thread: thread}, thread.ExpiresAt)
			continue
		}
		at, active := s.current.record.Starts[thread.Source]
		if !active || !s.current.comments[thread.Source] || thread.PostDate < at {
			continue
		}
		choose(work{key: "thread/" + thread.ID, kind: workThread, thread: thread}, thread.NextScanAt)
	}
	for _, job := range s.state.Jobs {
		if job.State == statePending || job.State == stateSubmitting || job.State == stateUncertain {
			choose(work{key: "job/" + job.ID, kind: workJob, job: job}, job.NextAttemptAt)
		}
	}
	if selected.key == "" {
		return work{}, false
	}
	s.active[selected.key] = true
	return selected, true
}

func (s *Service) perform(ctx context.Context, item work, resources ports.ListenResources) error {
	switch item.kind {
	case workSource:
		return s.scanSource(ctx, item.source, resources)
	case workThread:
		return s.scanThread(ctx, item.thread, resources)
	case workJob:
		return s.processJob(ctx, item.job, resources)
	default:
		return fmt.Errorf("未知监听任务")
	}
}

func (s *Service) completeWork(ctx context.Context, item work, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, item.key)
	if err == nil || ctx.Err() != nil {
		return
	}
	now := s.clock()
	delay := time.Minute
	var flood *ports.ListenFloodWait
	if errors.As(err, &flood) {
		delay = max(delay, flood.Wait)
		s.blockedUntil = now.Add(delay)
	}
	change := types.ListenMutation{}
	switch item.kind {
	case workSource:
		if s.current.record.Starts[item.source.Ref] != item.source.ActivatedAt {
			return
		}
		source := s.sourceLocked(item.source.Ref)
		source.LastError, source.NextScanAt = err.Error(), now.Add(delay)
		change.Sources = []types.ListenSourceRecord{source}
	case workThread:
		thread, exists := s.state.Threads[item.thread.ID]
		if !exists {
			return
		}
		thread.LastError, thread.NextScanAt = err.Error(), now.Add(delay)
		if errors.Is(err, ports.ErrListenNoDiscussion) || errors.Is(err, ports.ErrListenDeleted) {
			thread.NextScanAt = thread.ExpiresAt
		}
		change.Threads = []types.ListenThreadRecord{thread}
		if _, enabled := s.current.record.Starts[thread.Source]; enabled {
			source := s.sourceLocked(thread.Source)
			source.LastError = err.Error()
			change.Sources = []types.ListenSourceRecord{source}
		}
	case workJob:
		job := s.state.Jobs[item.job.ID]
		if job.State == stateAccepted || job.State == stateFiltered || job.State == stateObsolete || job.State == stateFailed {
			return
		}
		job.Attempts++
		job.LastError = err.Error()
		delay = max(delay, min(15*time.Minute, time.Duration(1<<min(job.Attempts, 8))*15*time.Second))
		job.NextAttemptAt = now.Add(delay)
		change.Jobs = []types.ListenJobRecord{job}
		if _, enabled := s.current.record.Starts[job.Source]; enabled {
			source := s.sourceLocked(job.Source)
			source.LastError = err.Error()
			if job.Attempts == 1 {
				source.Failed++
			}
			change.Sources = []types.ListenSourceRecord{source}
		}
	}
	if persistErr := s.persistLocked(ctx, change); persistErr != nil {
		s.lastError = persistErr.Error()
	}
	slog.Warn("监听任务等待重试", "component", ID, "account", s.account, "work", item.key, "error", err)
}
