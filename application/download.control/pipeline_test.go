package downloadcontrol

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const pipelineAccount = "pipeline-test"

type pipelineSource struct {
	items    []ports.DownloadMedia
	register func(string) error
	collect  func(context.Context) error
}

func (s *pipelineSource) Collect(ctx context.Context, _ types.DownloadIntent) ([]ports.DownloadMedia, ports.DownloadRegistration, error) {
	if s.collect != nil {
		if err := s.collect(ctx); err != nil {
			return nil, nil, err
		}
	}
	return s.items, s, nil
}

func (s *pipelineSource) Register(_ context.Context, token, _ string) (string, error) {
	if s.register != nil {
		if err := s.register(token); err != nil {
			return "", err
		}
	}
	return token, nil
}

func (*pipelineSource) URL(context.Context, string) (string, error) {
	return "https://localhost/file", nil
}

type pipelineFilter struct {
	reject func(ports.FilterInput) ports.Reason
}

func (f pipelineFilter) ShouldHandle(_ context.Context, in ports.FilterInput) (bool, ports.Reason) {
	if f.reject != nil {
		if reason := f.reject(in); reason != ports.Allowed {
			return false, reason
		}
	}
	return true, ports.Allowed
}

type pipelineNaming struct{ fail string }

func (n pipelineNaming) Render(_ context.Context, in ports.NamingInput) (ports.NamingResult, error) {
	if in.Data.FileName == n.fail {
		return ports.NamingResult{}, errors.New("invalid naming")
	}
	return ports.NamingResult{FileName: in.Data.FileName, Out: in.Data.FileName, Dir: in.BaseDir, FullPath: filepath.Join(in.BaseDir, in.Data.FileName)}, nil
}

func (pipelineNaming) Unique(_ context.Context, targets []ports.NamingResult) ([]ports.NamingResult, error) {
	return targets, nil
}

func pipelineResources(count int) ports.DownloadResources {
	source := &pipelineSource{}
	for i := 1; i <= count; i++ {
		token := strconv.Itoa(i)
		source.items = append(source.items, ports.DownloadMedia{Token: token, Data: ports.NamingData{MessageID: i, FileName: token, FileSize: 4}})
	}
	return ports.DownloadResources{Source: source, Filter: pipelineFilter{}, Naming: pipelineNaming{}, Files: remoteOnlyFiles{}, Executors: map[string]ports.DownloadExecutor{}, Defaults: ports.DownloadDefaults{RemoteRoot: "/remote", Limit: 2}}
}

type remoteOnlyFiles struct{}

func (remoteOnlyFiles) EnsureWritable(context.Context, string) error {
	panic("remote path reached local writable probe")
}

func (remoteOnlyFiles) EnsureDirectory(context.Context, string) error {
	panic("remote path reached local mkdir")
}

func (remoteOnlyFiles) SameFile(context.Context, string, int64) (bool, error) {
	panic("remote path reached local stat")
}

func pipelineHost(t *testing.T) (*rte.Runtime, ports.DownloadPipeline) {
	t.Helper()
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, nil))
	host, err := registry.Build(pipelineAccount, nil, map[string]map[string]any{ID: {executorsField: []string{aria2Executor}}})
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.DownloadPipelineName)
	require.NoError(t, err)
	return host, value.(ports.DownloadPipeline)
}

func TestPipelineReportsPartialAdmissionAndUncertainty(t *testing.T) {
	_, pipeline := pipelineHost(t)
	r := pipelineResources(7)
	r.Filter = pipelineFilter{reject: func(in ports.FilterInput) ports.Reason {
		if in.Name == "4" {
			return ports.ExtensionExcluded
		}
		return ports.Allowed
	}}
	r.Naming = pipelineNaming{fail: "5"}
	r.Source.(*pipelineSource).register = func(token string) error {
		if token == "6" {
			return errors.New("source save failed")
		}
		return nil
	}
	r.Executors[aria2Executor] = batchExecutor{name: aria2Executor, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
		switch in.TaskID {
		case "1":
			return types.DownloadResult{}, ports.ErrDownloadNotAccepted
		case "2":
			return types.DownloadResult{}, errors.New("RPC response lost")
		case "7":
			return types.DownloadResult{ID: "accepted"}, fmt.Errorf("accepted but observer failed: %w", ports.ErrDownloadNotAccepted)
		default:
			return types.DownloadResult{ID: in.TaskID, Target: aria2Executor}, nil
		}
	}}
	result, err := pipeline.SubmitBatch(context.Background(), types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
	require.ErrorContains(t, err, "RPC response lost")
	require.ErrorContains(t, err, "source save failed")
	require.ErrorContains(t, err, "invalid naming")
	require.Equal(t, 7, result.Total)
	require.Equal(t, 2, result.Queued)
	require.Equal(t, 1, result.Skipped)
	require.Equal(t, 3, result.Failed)
	require.Equal(t, 1, result.Uncertain)
}

func TestPipelineOwnsBoundedWorkersAndDrainsBeforeStop(t *testing.T) {
	host, pipeline := pipelineHost(t)
	r := pipelineResources(8)
	entered, canceled, release := make(chan struct{}, 8), make(chan struct{}, 8), make(chan struct{})
	var active, peak atomic.Int32
	r.Executors[aria2Executor] = batchExecutor{name: aria2Executor, submit: func(ctx context.Context, _ types.DownloadSubmission) (types.DownloadResult, error) {
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		defer active.Add(-1)
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return types.DownloadResult{}, ctx.Err()
	}}
	finished := make(chan submissionOutcome, 1)
	var summary types.DownloadSubmissionSummary
	go func() {
		var err error
		summary, err = pipeline.SubmitBatch(context.Background(), types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
		finished <- submissionOutcome{err: err}
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("batch did not start")
		}
	}
	require.EqualValues(t, 2, peak.Load())
	select {
	case <-finished:
		t.Fatal("batch returned before admission finished")
	default:
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(stopCtx), context.DeadlineExceeded)
	for range 2 {
		<-canceled
	}
	close(release)
	require.NoError(t, host.Stop(context.Background()))
	require.ErrorIs(t, (<-finished).err, context.Canceled)
	require.Equal(t, 2, summary.Uncertain)
	require.Equal(t, 6, summary.Failed)
	require.Zero(t, summary.Queued)
	require.Empty(t, entered, "queued work must not enter an executor after cancellation")
	_, err := pipeline.SubmitBatch(context.Background(), types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
	require.ErrorContains(t, err, "stopped")
}

func TestPipelineAccountAndCallerCancellationReachCollection(t *testing.T) {
	_, pipeline := pipelineHost(t)
	r := pipelineResources(1)
	entered, canceled := make(chan struct{}), make(chan struct{})
	r.Source.(*pipelineSource).collect = func(ctx context.Context) error { close(entered); <-ctx.Done(); close(canceled); return ctx.Err() }
	_, err := pipeline.SubmitBatch(context.Background(), types.DownloadIntent{Account: "other", MessageID: 1}, r)
	require.ErrorContains(t, err, "account")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := pipeline.SubmitBatch(ctx, types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
		done <- err
	}()
	<-entered
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach source")
	}
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestPipelineUnavailablePolicyIsNotAFilteredSuccess(t *testing.T) {
	_, pipeline := pipelineHost(t)
	r := pipelineResources(1)
	r.Filter = pipelineFilter{reject: func(ports.FilterInput) ports.Reason { return "filter_unavailable" }}
	result, err := pipeline.SubmitBatch(context.Background(), types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
	require.ErrorContains(t, err, "filter unavailable")
	require.Equal(t, 1, result.Failed)
	require.Zero(t, result.Skipped)
}

func TestPipelineCancellationAfterDefiniteRejectionIsNotUncertain(t *testing.T) {
	_, pipeline := pipelineHost(t)
	r := pipelineResources(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Executors[aria2Executor] = batchExecutor{name: aria2Executor, submit: func(context.Context, types.DownloadSubmission) (types.DownloadResult, error) {
		cancel()
		return types.DownloadResult{}, ports.ErrDownloadNotAccepted
	}}
	result, err := pipeline.SubmitBatch(ctx, types.DownloadIntent{Account: pipelineAccount, MessageID: 1}, r)
	require.Error(t, err)
	require.Equal(t, 1, result.Failed)
	require.Zero(t, result.Uncertain)
}

func TestPipelineUsesHotRouteForNextBatch(t *testing.T) {
	host, pipeline := pipelineHost(t)
	r := pipelineResources(1)
	var calls atomic.Int32
	r.Executors[aria2Executor] = batchExecutor{name: aria2Executor, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
		calls.Add(1)
		return types.DownloadResult{Target: aria2Executor, ID: in.TaskID}, nil
	}}
	request := types.DownloadIntent{Account: pipelineAccount, MessageID: 1}
	first, err := pipeline.SubmitBatch(context.Background(), request, r)
	require.NoError(t, err)
	require.Equal(t, 1, first.Queued)
	require.Empty(t, first.Links)
	require.NoError(t, host.ReconfigureBatch(context.Background(), map[string]map[string]any{ID: {executorsField: []string{httpExecutor}}}))
	second, err := pipeline.SubmitBatch(context.Background(), request, r)
	require.NoError(t, err)
	require.Equal(t, 1, second.Queued)
	require.Len(t, second.Links, 1)
	require.EqualValues(t, 1, calls.Load())
}
