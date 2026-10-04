package downloadlisten

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func (s *Service) resolveComment(ctx context.Context, job types.ListenJobRecord, transport ports.ListenTransport) (types.ListenJobRecord, bool, error) {
	rootID := job.Message.ThreadID
	if rootID == 0 {
		rootID = job.Message.ReplyTo
	}
	if rootID <= 0 {
		return job, false, nil
	}
	s.mu.Lock()
	for _, thread := range s.state.Threads {
		if reference(thread.Peer) == reference(job.Message.Peer) && thread.RootID == rootID {
			job.Source, job.PostDate = thread.Source, thread.PostDate
			eligible := s.eligibleLocked(job)
			s.mu.Unlock()
			return job, eligible, nil
		}
	}
	s.mu.Unlock()
	// A missing/expired cache is resolved on demand, including posts older than 24h.
	visited := map[int]bool{}
	var root types.ListenMessage
	for range 32 {
		if visited[rootID] {
			return job, false, nil
		}
		visited[rootID] = true
		message, err := transport.Message(ctx, job.Message.Peer, rootID)
		if err != nil {
			return job, false, err
		}
		root = message
		if root.ForwardPeer.Kind == kindChannel && root.ForwardID > 0 {
			break
		}
		next := root.ThreadID
		if next == 0 {
			next = root.ReplyTo
		}
		if next <= 0 {
			return job, false, nil
		}
		rootID = next
	}
	ref := reference(root.ForwardPeer)
	p := s.policySnapshot()
	date := root.ForwardDate
	if date <= 0 {
		date = root.Date
	}
	at, selected := p.record.Starts[ref]
	if !selected || !p.comments[ref] || date < at {
		return job, false, nil
	}
	info, err := transport.ResolveSource(ctx, ref, true)
	if err != nil && info.Peer.ID == 0 {
		return job, false, err
	}
	if !info.Broadcast {
		return job, false, nil
	}
	actual, err := transport.Discussion(ctx, info.Peer, root.ForwardID)
	if err != nil {
		return job, false, err
	}
	if reference(actual.Peer) != reference(job.Message.Peer) || actual.RootID != rootID {
		return job, false, nil
	}
	job.Source, job.PostDate = ref, date
	return job, true, nil
}

func (s *Service) processJob(ctx context.Context, job types.ListenJobRecord, resources ports.ListenResources) error {
	if job.Comment && job.Source == "" {
		resolved, eligible, err := s.resolveComment(ctx, job, resources.Transport)
		if errors.Is(err, ports.ErrListenDeleted) || errors.Is(err, ports.ErrListenNoDiscussion) {
			return s.finishJob(ctx, job, stateObsolete, err.Error())
		}
		if err != nil {
			return err
		}
		if !eligible {
			return s.finishJob(ctx, job, stateObsolete, "")
		}
		job = resolved
		s.mu.Lock()
		err = s.persistLocked(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}})
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	eligible := s.eligibleLocked(job)
	s.mu.Unlock()
	if !eligible {
		return s.finishJob(ctx, job, stateObsolete, "")
	}
	if job.State == stateSubmitting || job.State == stateUncertain {
		return s.reconcileAdmission(ctx, job, resources)
	}
	if job.Comment && job.Message.Pending && !s.clock().Before(time.Unix(job.PostDate, 0).Add(recoveryWindow)) {
		return s.finishJob(ctx, job, stateObsolete, "帖子补查期限已结束，等待视频就绪更新")
	}
	message, err := resources.Transport.Message(ctx, job.Message.Peer, job.Message.ID)
	if errors.Is(err, ports.ErrListenDeleted) {
		return s.finishJob(ctx, job, stateObsolete, err.Error())
	}
	if err != nil {
		return err
	}
	if message.Outgoing {
		return s.finishJob(ctx, job, stateObsolete, "")
	}
	if message.MediaID != job.Message.MediaID {
		if err := s.finishJob(ctx, job, stateObsolete, "消息媒体已更新"); err != nil {
			return err
		}
		if job.Comment {
			thread := types.ListenThreadRecord{Source: job.Source, PostDate: job.PostDate}
			return s.observeThreadMessage(ctx, thread, message)
		}
		return s.Observe(ctx, message)
	}
	if message.Pending {
		job.Message = message
		job.NextAttemptAt = s.clock().Add(15 * time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.persistLocked(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}})
	}
	if message.MediaID == "" || (job.Comment && !message.Video) {
		return s.finishJob(ctx, job, stateFiltered, "")
	}
	job.Message = message
	s.mu.Lock()
	if !s.eligibleLocked(job) || !resources.Enabled() {
		s.mu.Unlock()
		return s.finishJob(ctx, job, stateObsolete, "")
	}
	job.State = stateSubmitting
	err = s.persistLocked(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	source := types.DownloadSourceListen
	if job.Comment {
		source = types.DownloadSourceListenComment
	}
	result, err := resources.Downloads.Submit(ctx, types.DownloadIntent{
		Account: s.account, Peer: message.Peer, PeerID: message.Peer.ID, MessageID: message.ID,
		Source: source, ExpectedMediaID: message.MediaID, Link: fmt.Sprintf("%s/%d", reference(message.Peer), message.ID),
	})
	if ctx.Err() != nil {
		return ctx.Err()
	} // Durable stateSubmitting is reconciled on reconnect.
	if result.Queued > 0 {
		return s.finishJob(ctx, job, stateAccepted, "")
	}
	if result.Uncertain > 0 {
		job.NextAttemptAt = s.clock().Add(scanInterval)
		return s.finishJob(ctx, job, stateUncertain, "下载器接收结果待确认")
	}
	if err == nil && result.Failed == 0 {
		return s.finishJob(ctx, job, stateFiltered, "")
	}
	job.State = statePending
	s.mu.Lock()
	persistErr := s.persistLocked(ctx, types.ListenMutation{Jobs: []types.ListenJobRecord{job}})
	s.mu.Unlock()
	if persistErr != nil {
		return persistErr
	}
	if err == nil {
		err = ports.ErrDownloadNotAccepted
	}
	return err
}

func (s *Service) reconcileAdmission(ctx context.Context, job types.ListenJobRecord, resources ports.ListenResources) error {
	if resources.Accepted != nil {
		accepted, err := resources.Accepted(ctx, job.Message.MediaID)
		if err != nil {
			return err
		}
		if accepted {
			return s.finishJob(ctx, job, stateAccepted, "")
		}
	}
	job.NextAttemptAt = s.clock().Add(scanInterval)
	return s.finishJob(ctx, job, stateUncertain, "下载器接收结果待确认；保留记录以避免重复下载")
}
