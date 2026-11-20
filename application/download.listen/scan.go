package downloadlisten

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func spread(key string) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return time.Duration(hash.Sum32()%20) * time.Second
}

func (s *Service) scanSource(ctx context.Context, source types.ListenSourceRecord, resources ports.ListenResources) error {
	p := s.policySnapshot()
	if p.record.Starts[source.Ref] != source.ActivatedAt {
		return nil
	}
	info, err := resources.Transport.ResolveSource(ctx, source.Ref, p.comments[source.Ref])
	if err != nil && info.Peer.ID == 0 {
		return err
	}
	metadataError := err
	s.mu.Lock()
	if s.current.record.Starts[source.Ref] != source.ActivatedAt {
		s.mu.Unlock()
		return nil
	}
	current := s.sourceLocked(source.Ref)
	current.Info = info
	err = s.persistLocked(ctx, types.ListenMutation{Sources: []types.ListenSourceRecord{current}})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	floor := max(source.ActivatedAt, s.clock().Add(-recoveryWindow).Unix())
	offset, head := 0, source.Cursor
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !s.sourceActive(source.Ref, source.ActivatedAt, resources) {
			return nil
		}
		page, err := resources.Transport.History(ctx, info.Peer, offset, source.Cursor)
		if err != nil {
			return err
		}
		stop := false
		for _, message := range page.Messages {
			head = max(head, message.ID)
			if message.Date < floor || message.ID <= source.Cursor {
				stop = true
				continue
			}
			if err := s.Observe(ctx, message); err != nil {
				return err
			}
		}
		if stop || page.Done {
			break
		}
		if page.NextOffset <= 0 || page.NextOffset == offset {
			return fmt.Errorf("监听历史分页没有推进")
		}
		offset = page.NextOffset
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.record.Starts[source.Ref] != source.ActivatedAt {
		return nil
	}
	current = s.sourceLocked(source.Ref)
	current.Cursor, current.LastScanAt = head, s.clock()
	current.LastError = ""
	if metadataError != nil {
		current.LastError = metadataError.Error()
	}
	current.NextScanAt = s.clock().Add(scanInterval + spread(string(source.Ref)))
	return s.persistLocked(ctx, types.ListenMutation{Sources: []types.ListenSourceRecord{current}})
}

func (s *Service) sourceActive(ref types.ChatRef, at int64, resources ports.ListenResources) bool {
	if !resources.Enabled() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.current.record.Starts[ref] == at
}

func (s *Service) scanThread(ctx context.Context, thread types.ListenThreadRecord, resources ports.ListenResources) error {
	s.mu.Lock()
	if !s.clock().Before(thread.ExpiresAt) {
		err := s.persistLocked(ctx, types.ListenMutation{DeleteThreads: []string{thread.ID}})
		s.mu.Unlock()
		return err
	}
	p := s.current
	at := p.record.Starts[thread.Source]
	info := s.sourceLocked(thread.Source).Info
	s.mu.Unlock()
	if at <= 0 || thread.PostDate < at || !p.comments[thread.Source] {
		return nil
	}
	if thread.RootID == 0 {
		if info.Peer.ID == 0 {
			resolved, err := resources.Transport.ResolveSource(ctx, thread.Source, true)
			if err != nil {
				return err
			}
			info = resolved
		}
		resolved, err := resources.Transport.Discussion(ctx, info.Peer, thread.PostID)
		if err != nil {
			return err
		}
		thread.Peer, thread.RootID = resolved.Peer, resolved.RootID
	}
	offset, head := 0, thread.Cursor
	for {
		if !s.sourceActive(thread.Source, at, resources) {
			return nil
		}
		currentPolicy := s.policySnapshot()
		if !currentPolicy.comments[thread.Source] || !s.clock().Before(thread.ExpiresAt) {
			break
		}
		page, err := resources.Transport.Replies(ctx, thread.Peer, thread.RootID, offset, thread.Cursor)
		if err != nil {
			return err
		}
		for _, message := range page.Messages {
			head = max(head, message.ID)
			if message.ID == thread.RootID {
				continue
			}
			message.ThreadID = thread.RootID
			if err := s.observeThreadMessage(ctx, thread, message); err != nil {
				return err
			}
		}
		if page.Done {
			break
		}
		if page.NextOffset <= 0 || page.NextOffset == offset {
			return fmt.Errorf("评论分页没有推进")
		}
		offset = page.NextOffset
	}
	thread.Cursor, thread.LastScanAt, thread.LastError = head, s.clock(), ""
	thread.NextScanAt = minTime(s.clock().Add(scanInterval+spread(thread.ID)), thread.ExpiresAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.record.Starts[thread.Source] != at || !s.current.comments[thread.Source] {
		return nil
	}
	source := s.sourceLocked(thread.Source)
	source.LastCommentScanAt = thread.LastScanAt
	return s.persistLocked(ctx, types.ListenMutation{Threads: []types.ListenThreadRecord{thread}, Sources: []types.ListenSourceRecord{source}})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Service) observeThreadMessage(ctx context.Context, thread types.ListenThreadRecord, message types.ListenMessage) error {
	if message.Outgoing || (!message.Video && !message.Pending) || message.ID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at, active := s.current.record.Starts[thread.Source]
	if !active || !s.current.comments[thread.Source] || thread.PostDate < at {
		return nil
	}
	id := jobID(s.account, message)
	if old, exists := s.state.Jobs[id]; exists && old.State != statePending {
		return nil
	}
	job := types.ListenJobRecord{Version: types.ListenRecordVersion, ID: id, Message: message, Source: thread.Source, Comment: true, PostDate: thread.PostDate, State: statePending, NextAttemptAt: s.clock()}
	if err := s.persistLocked(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}}); err != nil {
		return err
	}
	s.signal()
	return nil
}
