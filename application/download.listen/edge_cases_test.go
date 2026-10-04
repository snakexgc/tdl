package downloadlisten

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestPendingVideoWaitsForReadinessAndDisableCancelsAdmission(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	rule := testRule("user:1")
	saveRules(t, host, store, rule)
	clock.advance(time.Second)
	message := types.ListenMessage{Peer: types.MessagePeer{Kind: kindUser, ID: 1}, ID: 7, Date: clock.now().Unix(), MediaID: testMedia, Video: true, Pending: true}
	transport := &fakeTransport{messages: map[string]types.ListenMessage{"user:1/7": message}}
	downloads := &fakeDownloads{result: types.DownloadSubmissionSummary{Total: 1, Queued: 1}}
	r := testResources(transport, downloads)
	require.NoError(t, s.Observe(context.Background(), message))
	id := jobID(types.DefaultAccount, message)
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Empty(t, downloads.requests)
	require.Equal(t, statePending, s.state.Jobs[id].State)
	message.Pending = false
	transport.messages["user:1/7"] = message
	require.NoError(t, s.Observe(context.Background(), message))
	rule.Enabled = false
	saveRules(t, host, store, rule)
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Empty(t, downloads.requests)
	require.Equal(t, stateObsolete, s.state.Jobs[id].State)
}

func TestFloodWaitBlocksSharedQueueAndDoesNotAdvanceCursor(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	saveRules(t, host, store, testRule(testChannel))
	source := s.sourceLocked(testChannel)
	item := work{key: "source/" + string(testChannel), kind: workSource, source: source}
	s.completeWork(context.Background(), item, &ports.ListenFloodWait{Wait: 7 * time.Minute, Err: errors.New("FLOOD_WAIT_420")})
	_, found := s.nextWork(true)
	require.False(t, found)
	require.Zero(t, s.state.Sources[testChannel].Cursor)
	clock.advance(7 * time.Minute)
	_, found = s.nextWork(true)
	require.True(t, found)
}

func TestStatusDoesNotExposeProtocolAccessHashes(t *testing.T) {
	store := configtest.NewStore()
	s, host, _ := testService(t, newJournal(), store)
	saveRules(t, host, store, testRule(testChannel))
	source := s.sourceLocked(testChannel)
	source.Info.Peer = types.MessagePeer{Kind: kindChannel, ID: 10, AccessHash: 100}
	source.Info.Discussion = types.MessagePeer{Kind: kindChannel, ID: 20, AccessHash: 200}
	s.state.Sources[testChannel] = source
	status := s.Status()
	require.Zero(t, status.Sources[0].Info.Peer.AccessHash)
	require.Zero(t, status.Sources[0].Info.Discussion.AccessHash)
	require.Equal(t, int64(100), s.state.Sources[testChannel].Info.Peer.AccessHash)
}

func TestStatusListsEveryAdmissionWithoutLeakingProtocolDataAfterDisable(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	rule := testRule("user:1")
	saveRules(t, host, store, rule)
	clock.advance(time.Second)
	message := types.ListenMessage{Peer: types.MessagePeer{Kind: kindUser, ID: 1, AccessHash: 123456789}, ID: 1, Date: clock.now().Unix(), MediaID: testMedia, FileName: "one.mp4", FileSize: 1024}
	require.NoError(t, s.Observe(context.Background(), message))
	require.NoError(t, s.finishJob(context.Background(), s.state.Jobs[jobID(types.DefaultAccount, message)], stateAccepted, ""))
	message.ID, message.MediaID, message.FileName = 2, testOtherMedia, "filtered.mp4"
	clock.advance(time.Second)
	message.Date = clock.now().Unix()
	require.NoError(t, s.Observe(context.Background(), message))
	require.NoError(t, s.finishJob(context.Background(), s.state.Jobs[jobID(types.DefaultAccount, message)], stateFiltered, ""))
	rule.Enabled = false
	saveRules(t, host, store, rule)
	status := s.Status()
	require.Empty(t, status.Sources)
	require.Len(t, status.Tasks, 2)
	require.Equal(t, "filtered.mp4", status.Tasks[0].FileName)
	require.Equal(t, stateFiltered, status.Tasks[0].State)
	require.Equal(t, types.ChatRef("user:1"), status.Tasks[1].Source)
	require.Equal(t, int64(1024), status.Tasks[1].Total)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "123456789")
}

type partialMetadataTransport struct{ *fakeTransport }

func (f partialMetadataTransport) ResolveSource(ctx context.Context, ref types.ChatRef, comments bool) (types.ListenPeer, error) {
	info, err := f.fakeTransport.ResolveSource(ctx, ref, comments)
	if err != nil {
		return info, err
	}
	return info, ports.ErrListenInaccessible
}

func TestRealtimeCommentsRecoverMissingMetadataAndRejectUnrelatedThreads(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	rule := testRule(testChannel)
	rule.ListenComments = true
	saveRules(t, host, store, rule)
	clock.advance(time.Second)
	channel := types.MessagePeer{Kind: kindChannel, ID: 10}
	group := types.MessagePeer{Kind: kindChannel, ID: 20}
	info := types.ListenPeer{Peer: channel, Broadcast: true}
	source := s.sourceLocked(testChannel)
	source.Info = info
	s.state.Sources[testChannel] = source
	post := types.ListenMessage{Peer: channel, ID: 30, Date: clock.now().Unix(), Broadcast: true}
	thread := types.ListenThreadRecord{Version: types.ListenRecordVersion, ID: threadID(testChannel, post.ID), Source: testChannel, PostID: post.ID, PostDate: post.Date, Peer: group, RootID: 300, ExpiresAt: clock.now().Add(recoveryWindow)}
	s.state.Threads[thread.ID] = thread
	message := types.ListenMessage{Peer: group, ID: 301, Date: post.Date, ThreadID: 300, MediaID: testMedia, Video: true}
	transport := &fakeTransport{info: map[types.ChatRef]types.ListenPeer{testChannel: info}, messages: map[string]types.ListenMessage{"channel:20/301": message}}
	transport.discussion = func(context.Context, types.MessagePeer, int) (types.ListenThreadRecord, error) {
		return types.ListenThreadRecord{Peer: group, RootID: 300}, nil
	}
	downloads := &fakeDownloads{result: types.DownloadSubmissionSummary{Total: 1, Queued: 1}}
	r := testResources(transport, downloads)
	r.Transport = partialMetadataTransport{transport}
	require.NoError(t, s.Observe(context.Background(), message))
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[jobID(types.DefaultAccount, message)], r))
	require.Len(t, downloads.requests, 1, "persisted thread mapping survives missing linked group metadata")

	delete(s.state.Threads, thread.ID)
	message.ID++
	transport.messages["channel:20/302"] = message
	transport.messages["channel:20/300"] = types.ListenMessage{Peer: group, ID: 300, Date: post.Date, ForwardPeer: channel, ForwardID: post.ID, ForwardDate: post.Date}
	require.NoError(t, s.Observe(context.Background(), message))
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[jobID(types.DefaultAccount, message)], r))
	require.Len(t, downloads.requests, 2, "on-demand discussion lookup uses a readable channel despite optional metadata failure")

	message.ID, message.ThreadID = 303, 400
	transport.messages["channel:20/303"] = message
	transport.messages["channel:20/400"] = types.ListenMessage{Peer: group, ID: 400, Date: post.Date, ForwardPeer: channel, ForwardID: post.ID, ForwardDate: post.Date}
	require.NoError(t, s.Observe(context.Background(), message))
	id := jobID(types.DefaultAccount, message)
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Equal(t, stateObsolete, s.state.Jobs[id].State)
	require.Len(t, downloads.requests, 2, "a forwarded post outside the actual discussion thread is rejected")
	message.ID, message.Video = 304, false
	require.NoError(t, s.Observe(context.Background(), message))
	_, exists := s.state.Jobs[jobID(types.DefaultAccount, message)]
	require.False(t, exists, "non-video replies do not create admission jobs")
}
