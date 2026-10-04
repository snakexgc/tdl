package downloadlisten

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

func jobID(account types.AccountID, message types.ListenMessage) string {
	return fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%s/%s/%d/%s", account, reference(message.Peer), message.ID, message.MediaID)))
}

// Observe commits admission before the update manager may advance its PTS.
func (s *Service) Observe(ctx context.Context, message types.ListenMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Outgoing || message.ID <= 0 || message.Date <= 0 || !validRef(reference(message.Peer)) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.current == nil {
		return nil
	}
	ref := reference(message.Peer)
	at, direct := s.current.record.Starts[ref]
	if direct && message.Date < at {
		return nil
	}
	comment := !direct
	if comment && !s.possibleCommentLocked(message) {
		return nil
	}
	change := types.ListenMutation{}
	if direct {
		source := s.sourceLocked(ref)
		if message.Date > source.LastMessageAt {
			source.LastMessageAt = message.Date
		}
		change.Sources = append(change.Sources, source)
		if s.current.comments[ref] && (message.Broadcast || source.Info.Broadcast) {
			if thread, ok := s.newThreadLocked(ref, message); ok {
				change.Threads = append(change.Threads, thread)
			}
		}
	}
	// Text posts still create a thread; ordinary text messages need no admission job.
	if direct && message.MediaID == "" && !message.Pending {
		if err := s.persistLocked(ctx, change); err != nil {
			return err
		}
		s.signal()
		return nil
	}
	id := jobID(s.account, message)
	if previous, exists := s.state.Jobs[id]; exists {
		canRevive := previous.State == stateObsolete && previous.Message.Pending && !message.Pending
		if previous.State != statePending && !canRevive {
			return nil
		}
		previous.State = statePending
		previous.NextAttemptAt = s.clock()
		previous.Message = message
		change.Jobs = []types.ListenJobRecord{previous}
	} else {
		job := types.ListenJobRecord{Version: types.ListenRecordVersion, ID: id, Message: message, State: statePending, NextAttemptAt: s.clock(), Comment: comment}
		if direct {
			job.Source = ref
		}
		change.Jobs = []types.ListenJobRecord{job}
	}
	if err := s.persistLocked(ctx, change); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *Service) possibleCommentLocked(message types.ListenMessage) bool {
	if message.Peer.Kind != kindChannel || (message.ReplyTo == 0 && message.ThreadID == 0) || (!message.Video && !message.Pending) {
		return false
	}
	root := message.ThreadID
	if root == 0 {
		root = message.ReplyTo
	}
	for _, thread := range s.state.Threads {
		at, active := s.current.record.Starts[thread.Source]
		if active && s.current.comments[thread.Source] && thread.PostDate >= at && thread.RootID == root && reference(thread.Peer) == reference(message.Peer) {
			return true
		}
	}
	unknown := false
	for ref, comments := range s.current.comments {
		if !comments || len(ref) < 8 || string(ref[:8]) != "channel:" {
			continue
		}
		info := s.sourceLocked(ref).Info
		if info.Peer.ID == 0 || (info.Broadcast && info.Discussion.ID == 0) {
			unknown = true
		}
		if info.Broadcast && reference(info.Discussion) == reference(message.Peer) {
			return true
		}
	}
	return unknown
}

func (s *Service) newThreadLocked(source types.ChatRef, message types.ListenMessage) (types.ListenThreadRecord, bool) {
	id := threadID(source, message.ID)
	if _, exists := s.state.Threads[id]; exists {
		return types.ListenThreadRecord{}, false
	}
	expires := time.Unix(message.Date, 0).Add(recoveryWindow)
	if !s.clock().Before(expires) {
		return types.ListenThreadRecord{}, false
	}
	return types.ListenThreadRecord{Version: types.ListenRecordVersion, ID: id, Source: source, PostID: message.ID, PostDate: message.Date, ExpiresAt: expires, NextScanAt: s.clock()}, true
}

func (s *Service) eligibleLocked(job types.ListenJobRecord) bool {
	at, enabled := s.current.record.Starts[job.Source]
	if !enabled {
		return false
	}
	if !job.Comment {
		return job.Message.Date >= at
	}
	return s.current.comments[job.Source] && job.PostDate >= at
}

func (s *Service) finishJob(ctx context.Context, job types.ListenJobRecord, state, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.State, job.LastError = state, message
	change := types.ListenMutation{Jobs: []types.ListenJobRecord{job}}
	if _, enabled := s.current.record.Starts[job.Source]; enabled {
		source := s.sourceLocked(job.Source)
		previous := s.state.Jobs[job.ID].State
		if previous != state {
			switch state {
			case stateAccepted:
				source.Queued++
			case stateFiltered:
				source.Filtered++
			case stateFailed, stateUncertain:
				source.Failed++
			}
		}
		if message != "" {
			source.LastError = message
		} else if state == stateAccepted && source.LastError == s.state.Jobs[job.ID].LastError {
			source.LastError = ""
		}
		change.Sources = []types.ListenSourceRecord{source}
	}
	return s.persistLocked(ctx, change)
}
