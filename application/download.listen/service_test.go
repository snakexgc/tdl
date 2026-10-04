package downloadlisten

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

const (
	testChannel    types.ChatRef = "channel:10"
	testMedia      string        = "document_900"
	testOtherMedia string        = "document_901"
)

// The component test implements only its repository port; adapter persistence
// and namespace isolation are exercised in bsw/cdd/listen and app/webui.
type testJournal struct {
	mu    sync.Mutex
	state types.ListenState
}

func newJournal() *testJournal {
	return &testJournal{state: types.ListenState{Sources: map[types.ChatRef]types.ListenSourceRecord{}, Threads: map[string]types.ListenThreadRecord{}, Jobs: map[string]types.ListenJobRecord{}}}
}

func (j *testJournal) Load(ctx context.Context) (types.ListenState, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return types.ListenState{}, err
	}
	raw, err := json.Marshal(j.state)
	if err != nil {
		return types.ListenState{}, err
	}
	var state types.ListenState
	err = json.Unmarshal(raw, &state)
	return state, err
}

func (j *testJournal) Update(ctx context.Context, change types.ListenMutation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if change.Policy != nil {
		j.state.Policy = *change.Policy
	}
	if change.PendingPolicy != nil {
		j.state.PendingPolicy = *change.PendingPolicy
	}
	for _, source := range change.Sources {
		j.state.Sources[source.Ref] = source
	}
	for _, thread := range change.Threads {
		j.state.Threads[thread.ID] = thread
	}
	for _, id := range change.DeleteThreads {
		delete(j.state.Threads, id)
	}
	for _, job := range change.Jobs {
		j.state.Jobs[job.ID] = job
	}
	return nil
}

type testClock struct{ value atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.value.Load()) }
func (c *testClock) advance(d time.Duration) { c.value.Add(int64(d)) }

func testService(t *testing.T, repository ports.ListenRepository, store *config.Store) (*Service, *rte.Runtime, *testClock) {
	t.Helper()
	s := New(repository, types.ListenFilters{DownloadEnabled: true})
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(Manifest(), func() rte.Component { return s }))
	doc, err := store.Load(context.Background(), ID)
	require.NoError(t, err)
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: doc.Values})
	require.NoError(t, err)
	for _, status := range host.Start(context.Background()) {
		require.Equal(t, rte.Running, status.State, status.Detail)
	}
	clock := &testClock{}
	clock.value.Store(time.Now().Add(time.Second).UnixNano())
	s.clock = clock.now
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	return s, host, clock
}

func saveRules(t *testing.T, host *rte.Runtime, store *config.Store, rules ...types.DownloadListenRule) {
	t.Helper()
	if rules == nil {
		rules = []types.DownloadListenRule{}
	}
	require.NoError(t, host.ReconfigureSaved(context.Background(), ID, map[string]any{rulesField: rules}, store))
}

func testRule(sources ...types.ChatRef) types.DownloadListenRule {
	return types.DownloadListenRule{ID: "test-rule", Name: "测试监听", Enabled: true, Sources: sources}
}

type fakeTransport struct {
	info       map[types.ChatRef]types.ListenPeer
	messages   map[string]types.ListenMessage
	history    func(context.Context, types.MessagePeer, int, int) (types.ListenPage, error)
	replies    func(context.Context, types.MessagePeer, int, int, int) (types.ListenPage, error)
	discussion func(context.Context, types.MessagePeer, int) (types.ListenThreadRecord, error)
}

func (f *fakeTransport) ResolveSource(_ context.Context, ref types.ChatRef, _ bool) (types.ListenPeer, error) {
	if info, ok := f.info[ref]; ok {
		return info, nil
	}
	return types.ListenPeer{}, ports.ErrListenInaccessible
}

func (f *fakeTransport) History(ctx context.Context, peer types.MessagePeer, offset, minID int) (types.ListenPage, error) {
	if f.history == nil {
		return types.ListenPage{Done: true}, nil
	}
	return f.history(ctx, peer, offset, minID)
}

func (f *fakeTransport) Replies(ctx context.Context, peer types.MessagePeer, root, offset, minID int) (types.ListenPage, error) {
	if f.replies == nil {
		return types.ListenPage{Done: true}, nil
	}
	return f.replies(ctx, peer, root, offset, minID)
}

func (f *fakeTransport) Discussion(ctx context.Context, peer types.MessagePeer, id int) (types.ListenThreadRecord, error) {
	if f.discussion == nil {
		return types.ListenThreadRecord{}, ports.ErrListenNoDiscussion
	}
	return f.discussion(ctx, peer, id)
}

func (f *fakeTransport) Message(_ context.Context, peer types.MessagePeer, id int) (types.ListenMessage, error) {
	if message, exists := f.messages[fmt.Sprintf("%s/%d", reference(peer), id)]; exists {
		return message, nil
	}
	return types.ListenMessage{}, ports.ErrListenDeleted
}

type fakeDownloads struct {
	mu       sync.Mutex
	requests []types.DownloadIntent
	result   types.DownloadSubmissionSummary
	err      error
}

func (f *fakeDownloads) Submit(_ context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func testResources(transport *fakeTransport, downloads *fakeDownloads) ports.ListenResources {
	return ports.ListenResources{Transport: transport, Downloads: downloads, Enabled: func() bool { return true }}
}

func TestLiveRulesSaveRollbackConflictAndActivationRecovery(t *testing.T) {
	ctx := context.Background()
	repository := newJournal()
	file := &configtest.Repository{}
	store := config.NewManaged(file)
	s, host, clock := testService(t, repository, store)
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: Manifest(), Scope: rte.AccountScope, LiveValues: true})
	require.NoError(t, err)
	snapshot, err := store.Snapshot(ctx, []string{ID})
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, store).WithActiveStore(snapshot)
	require.NoError(t, directory.Bind("listen", func() *rte.Runtime { return host }))
	rule := testRule(testChannel, "chat:2", "user:3", "user:4")
	revision, err := store.Revision(ctx, ID)
	require.NoError(t, err)
	require.NoError(t, directory.PatchWithRevision(ctx, ID, map[string]any{rulesField: []types.DownloadListenRule{rule}}, revision))
	require.Len(t, s.Status().Sources, 4)
	require.False(t, directory.Configurations(ctx)[0].PendingRestart)
	at := s.current.record.Starts[testChannel]
	require.ErrorIs(t, directory.PatchWithRevision(ctx, ID, nil, revision), rte.ErrConfigurationConflict)
	clock.advance(time.Minute)
	rule.Name = "改名保持进度"
	saveRules(t, host, store, rule)
	require.Equal(t, at, s.current.record.Starts[testChannel])
	file.SaveError = errors.New("disk full")
	rule.Sources = []types.ChatRef{"user:99"}
	require.ErrorContains(t, host.ReconfigureSaved(ctx, ID, map[string]any{rulesField: []types.DownloadListenRule{rule}}, store), "disk full")
	require.NotContains(t, s.current.record.Starts, types.ChatRef("user:99"))
	state, err := repository.Load(ctx)
	require.NoError(t, err)
	require.Empty(t, state.PendingPolicy.Signature)
	file.SaveError = nil
	rule.Sources = []types.ChatRef{testChannel}
	rule.Enabled = false
	saveRules(t, host, store, rule)
	clock.advance(time.Minute)
	rule.Enabled = true
	saveRules(t, host, store, rule)
	require.Greater(t, s.current.record.Starts[testChannel], at)
	// Crash between config-file persistence and live publication: pending starts win.
	clock.advance(time.Minute)
	rule.Sources = append(rule.Sources, "user:99")
	view, err := config.New(Manifest().Config, map[string]any{rulesField: []types.DownloadListenRule{rule}})
	require.NoError(t, err)
	_, err = s.PrepareConfig(ctx, view)
	require.NoError(t, err)
	_, err = s.StageConfig(ctx, view)
	require.NoError(t, err)
	expected := s.prepared.record.Starts["user:99"]
	require.NoError(t, store.Save(ctx, ID, true, view))
	reopened, _, _ := testService(t, repository, store)
	require.Equal(t, expected, reopened.current.record.Starts["user:99"])
}

func TestJournalIncomingSourcesOverlapEditAndRestartDeduplication(t *testing.T) {
	repository := newJournal()
	store := configtest.NewStore()
	s, host, clock := testService(t, repository, store)
	rule := testRule("chat:1", "channel:1", "user:1", "user:2")
	duplicate := rule
	duplicate.ID = "overlap"
	saveRules(t, host, store, rule, duplicate)
	clock.advance(time.Second)
	transport := &fakeTransport{messages: map[string]types.ListenMessage{}}
	downloads := &fakeDownloads{result: types.DownloadSubmissionSummary{Total: 1, Queued: 1}}
	for index, peer := range []types.MessagePeer{{Kind: kindChat, ID: 1}, {Kind: kindChannel, ID: 1}, {Kind: kindUser, ID: 1}, {Kind: kindUser, ID: 2}} {
		message := types.ListenMessage{Peer: peer, ID: 100, Date: clock.now().Unix(), MediaID: fmt.Sprintf("document_%d", index+1)}
		require.NoError(t, s.Observe(context.Background(), message))
		require.NoError(t, s.Observe(context.Background(), message))
		outgoing := message
		outgoing.ID++
		outgoing.Outgoing = true
		require.NoError(t, s.Observe(context.Background(), outgoing))
		old := message
		old.ID += 10
		old.Date = s.current.record.Starts[reference(peer)] - 1
		require.NoError(t, s.Observe(context.Background(), old))
		transport.messages[fmt.Sprintf("%s/%d", reference(peer), message.ID)] = message
	}
	require.Len(t, s.state.Jobs, 4)
	for _, job := range s.state.Jobs {
		require.NoError(t, s.processJob(context.Background(), job, testResources(transport, downloads)))
	}
	require.Len(t, downloads.requests, 4)
	reopened, _, _ := testService(t, repository, store)
	for _, message := range transport.messages {
		require.NoError(t, reopened.Observe(context.Background(), message))
	}
	for _, job := range reopened.state.Jobs {
		require.Equal(t, stateAccepted, job.State)
	}
	message := transport.messages["user:1/100"]
	message.MediaID = testMedia
	transport.messages["user:1/100"] = message
	require.NoError(t, reopened.Observe(context.Background(), message))
	require.Len(t, reopened.state.Jobs, 5)
	require.NoError(t, reopened.processJob(context.Background(), reopened.state.Jobs[jobID(types.DefaultAccount, message)], testResources(transport, downloads)))
	require.Len(t, downloads.requests, 5)
}

func TestHistoryRecoveryWindowAndInterruptedPagination(t *testing.T) {
	s, host, clock := testService(t, newJournal(), configtest.NewStore())
	store := configtest.NewStore()
	saveRules(t, host, store, testRule(testChannel))
	clock.advance(48 * time.Hour)
	peer := types.MessagePeer{Kind: kindChannel, ID: 10}
	transport := &fakeTransport{info: map[types.ChatRef]types.ListenPeer{testChannel: {Peer: peer, Broadcast: true}}}
	broken := true
	transport.history = func(_ context.Context, _ types.MessagePeer, offset, minID int) (types.ListenPage, error) {
		require.Equal(t, 0, minID)
		if offset == 0 {
			return types.ListenPage{NextOffset: 200, Messages: []types.ListenMessage{{Peer: peer, ID: 200, Date: clock.now().Unix(), MediaID: testMedia}}}, nil
		}
		if broken {
			return types.ListenPage{}, errors.New("page interrupted")
		}
		return types.ListenPage{Done: true, Messages: []types.ListenMessage{{Peer: peer, ID: 199, Date: clock.now().Add(-2 * time.Hour).Unix(), MediaID: testOtherMedia}, {Peer: peer, ID: 198, Date: clock.now().Add(-25 * time.Hour).Unix(), MediaID: "document_902"}}}, nil
	}
	source := s.sourceLocked(testChannel)
	r := testResources(transport, &fakeDownloads{})
	require.ErrorContains(t, s.scanSource(context.Background(), source, r), "page interrupted")
	require.Zero(t, s.state.Sources[testChannel].Cursor)
	require.Len(t, s.state.Jobs, 1)
	broken = false
	require.NoError(t, s.scanSource(context.Background(), source, r))
	require.Equal(t, 200, s.state.Sources[testChannel].Cursor)
	require.Len(t, s.state.Jobs, 2)
}

func TestCommentDelayNestedPaginationExpiryAndRealtime(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	rule := testRule(testChannel)
	rule.ListenComments = true
	saveRules(t, host, store, rule)
	clock.advance(time.Second)
	channel := types.MessagePeer{Kind: kindChannel, ID: 10}
	group := types.MessagePeer{Kind: kindChannel, ID: 20}
	post := types.ListenMessage{Peer: channel, ID: 30, Date: clock.now().Unix(), Broadcast: true}
	require.NoError(t, s.Observe(context.Background(), post))
	transport := &fakeTransport{info: map[types.ChatRef]types.ListenPeer{testChannel: {Peer: channel, Broadcast: true, Discussion: group}}, messages: map[string]types.ListenMessage{}}
	transport.discussion = func(_ context.Context, _ types.MessagePeer, id int) (types.ListenThreadRecord, error) {
		require.Equal(t, 30, id)
		return types.ListenThreadRecord{Peer: group, RootID: 300}, nil
	}
	downloads := &fakeDownloads{result: types.DownloadSubmissionSummary{Total: 1, Queued: 1}}
	r := testResources(transport, downloads)
	require.NoError(t, s.scanSource(context.Background(), s.sourceLocked(testChannel), r))
	require.NoError(t, s.scanThread(context.Background(), s.state.Threads[threadID(testChannel, 30)], r))
	require.Empty(t, s.state.Jobs, "text-only post is tracked before comments appear")
	clock.advance(scanInterval)
	calls := 0
	transport.replies = func(_ context.Context, _ types.MessagePeer, root, offset, minID int) (types.ListenPage, error) {
		calls++
		require.Equal(t, 300, root)
		require.Zero(t, minID)
		page := types.ListenPage{NextOffset: 306}
		start, end := 405, 306
		if offset != 0 {
			start, end, page.Done = 305, 301, true
		}
		for id := start; id >= end; id-- {
			message := types.ListenMessage{Peer: group, ID: id, Date: clock.now().Unix(), ThreadID: root, ReplyTo: 304, MediaID: fmt.Sprintf("document_%d", id), Video: id%2 == 0}
			page.Messages = append(page.Messages, message)
			transport.messages[fmt.Sprintf("%s/%d", reference(group), id)] = message
		}
		return page, nil
	}
	require.NoError(t, s.scanThread(context.Background(), s.state.Threads[threadID(testChannel, 30)], r))
	require.Equal(t, 2, calls)
	require.Len(t, s.state.Jobs, 52)
	for _, job := range s.state.Jobs {
		require.NoError(t, s.processJob(context.Background(), job, r))
	}
	require.Len(t, downloads.requests, 52)
	for _, request := range downloads.requests {
		require.Equal(t, types.DownloadSourceListenComment, request.Source)
	}
	clock.advance(25 * time.Hour)
	require.NoError(t, s.scanThread(context.Background(), s.state.Threads[threadID(testChannel, 30)], r))
	require.Equal(t, 2, calls, "expired thread makes no requests")
	require.Empty(t, s.state.Threads)
	transport.messages["channel:20/300"] = types.ListenMessage{Peer: group, ID: 300, Date: post.Date, ForwardPeer: channel, ForwardID: 30, ForwardDate: post.Date}
	late := types.ListenMessage{Peer: group, ID: 999, Date: clock.now().Unix(), ThreadID: 300, MediaID: testMedia, Video: true}
	transport.messages["channel:20/999"] = late
	require.NoError(t, s.Observe(context.Background(), late))
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[jobID(types.DefaultAccount, late)], r))
	require.Len(t, downloads.requests, 53)
}

func TestUncertainAdmissionDoesNotResubmitAndDefiniteFailureRetries(t *testing.T) {
	store := configtest.NewStore()
	s, host, clock := testService(t, newJournal(), store)
	saveRules(t, host, store, testRule("user:1"))
	clock.advance(time.Second)
	message := types.ListenMessage{Peer: types.MessagePeer{Kind: kindUser, ID: 1}, ID: 20, Date: clock.now().Unix(), MediaID: testMedia}
	transport := &fakeTransport{messages: map[string]types.ListenMessage{"user:1/20": message}}
	downloads := &fakeDownloads{result: types.DownloadSubmissionSummary{Total: 1, Uncertain: 1}, err: errors.New("response lost")}
	r := testResources(transport, downloads)
	require.NoError(t, s.Observe(context.Background(), message))
	id := jobID(types.DefaultAccount, message)
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Equal(t, stateUncertain, s.state.Jobs[id].State)
	r.Accepted = func(context.Context, string) (bool, error) { return false, nil }
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Len(t, downloads.requests, 1)
	require.Equal(t, 1, s.state.Sources["user:1"].Failed)
	r.Accepted = func(context.Context, string) (bool, error) { return true, nil }
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Equal(t, stateAccepted, s.state.Jobs[id].State)
	message.ID++
	message.MediaID = testOtherMedia
	transport.messages["user:1/21"] = message
	require.NoError(t, s.Observe(context.Background(), message))
	id = jobID(types.DefaultAccount, message)
	downloads.result, downloads.err = types.DownloadSubmissionSummary{Total: 1, Failed: 1}, ports.ErrDownloadNotAccepted
	require.ErrorIs(t, s.processJob(context.Background(), s.state.Jobs[id], r), ports.ErrDownloadNotAccepted)
	require.Equal(t, statePending, s.state.Jobs[id].State)
	downloads.result, downloads.err = types.DownloadSubmissionSummary{Total: 1, Queued: 1}, nil
	require.NoError(t, s.processJob(context.Background(), s.state.Jobs[id], r))
	require.Equal(t, stateAccepted, s.state.Jobs[id].State)
}

func TestSharedWorkersBoundConcurrencyAndDrainOnDisconnect(t *testing.T) {
	store := configtest.NewStore()
	s, host, _ := testService(t, newJournal(), store)
	saveRules(t, host, store, testRule("user:1", "user:2", "user:3"))
	transport := &fakeTransport{info: map[types.ChatRef]types.ListenPeer{}}
	for id := int64(1); id <= 3; id++ {
		transport.info[types.ChatRef(fmt.Sprintf("user:%d", id))] = types.ListenPeer{Peer: types.MessagePeer{Kind: kindUser, ID: id}}
	}
	var active, peak atomic.Int32
	transport.history = func(ctx context.Context, _ types.MessagePeer, _, _ int) (types.ListenPage, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, n) {
				break
			}
		}
		<-ctx.Done()
		return types.ListenPage{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, testResources(transport, &fakeDownloads{})) }()
	require.Eventually(t, func() bool { return peak.Load() == 2 }, time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("listener did not drain")
	}
	require.Zero(t, active.Load())
	require.False(t, s.Status().Connected)
}

func TestRulesRejectInvalidDisabledRulesAndUnknownNestedFields(t *testing.T) {
	for _, rules := range []any{
		[]types.DownloadListenRule{testRule("channel:0")},
		[]types.DownloadListenRule{{ID: "disabled", Sources: []types.ChatRef{"self"}}},
		[]types.DownloadListenRule{testRule(testChannel), testRule("user:1")},
		[]map[string]any{{"id": "unknown", "sources": []string{"user:1"}, "extra": true}},
	} {
		view, err := config.New(Manifest().Config, map[string]any{rulesField: rules})
		require.NoError(t, err)
		_, err = New(nil, types.ListenFilters{}).PrepareConfig(context.Background(), view)
		require.Error(t, err)
	}
}
