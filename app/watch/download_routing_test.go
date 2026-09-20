package watch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/services/localfs"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const testAria2Executor = "aria2"

type fixedDownloadRoute struct{ route ports.DownloadRoute }

func (f *fixedDownloadRoute) Route(context.Context, types.AccountID) (ports.DownloadRoute, error) {
	return f.route, nil
}

type preparedExecutor struct {
	name   string
	submit func(context.Context, types.DownloadSubmission) (types.DownloadResult, error)
}

func (e preparedExecutor) Name() string { return e.name }
func (e preparedExecutor) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	return e.submit(ctx, in)
}

type batchSourceFixture struct {
	media    []ports.DownloadMedia
	register func()
}

func (s *batchSourceFixture) Collect(context.Context, types.DownloadIntent) ([]ports.DownloadMedia, ports.DownloadRegistration, error) {
	return s.media, s, nil
}

func (s *batchSourceFixture) Register(_ context.Context, token, _ string) (string, error) {
	if s.register != nil {
		s.register()
	}
	return token, nil
}

func (*batchSourceFixture) URL(context.Context, string) (string, error) {
	return "http://localhost:8090/file", nil
}

type (
	changingNaming     struct{ ports.NamingRules }
	rejectRemoteNaming struct {
		ports.NamingRules
		root string
	}
)

func (n rejectRemoteNaming) Render(ctx context.Context, in ports.NamingInput) (ports.NamingResult, error) {
	if in.BaseDir == n.root {
		return ports.NamingResult{}, errors.New("remote naming unavailable")
	}
	return n.NamingRules.Render(ctx, in)
}

func pipelineFixture(t *testing.T) (ports.DownloadPipeline, ports.DownloadResources, types.DownloadIntent) {
	t.Helper()
	host, control, err := application.DownloadControlHost(context.Background(), types.DefaultAccount, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	w := namingWatcher(t, "F", 255)
	source := &batchSourceFixture{media: []ports.DownloadMedia{{Token: "task", Data: ports.NamingData{MessageID: 2, FileName: testVideoFile, FileSize: 4}}}}
	r := ports.DownloadResources{
		Source: source, Filter: w.opts.Filter, Naming: w.opts.Naming, Files: localfs.Downloads{},
		Executors: map[string]ports.DownloadExecutor{}, Defaults: ports.DownloadDefaults{Limit: 1, RemoteRoot: filepath.Join(t.TempDir(), "remote-only")},
	}
	return control.(ports.DownloadPipeline), r, types.DownloadIntent{Account: types.DefaultAccount, MessageID: 2}
}

func TestModeSwitchDoesNotReroutePreparedLocalTask(t *testing.T) {
	pipeline, resources, request := pipelineFixture(t)
	root := t.TempDir()
	route := &fixedDownloadRoute{ports.DownloadRoute{Executors: []string{localExecutorName}, LocalRoot: root}}
	resources.Routing = route
	resources.Source.(*batchSourceFixture).register = func() { route.route.Executors = []string{testAria2Executor} }
	called := false
	resources.Executors[localExecutorName] = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
		called = true
		require.Equal(t, root, in.Dir)
		return types.DownloadResult{Account: in.Account, Target: localExecutorName, ID: in.TaskID}, nil
	}}
	result, err := pipeline.SubmitBatch(context.Background(), request, resources)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, 1, result.Queued)
	require.NoDirExists(t, resources.Defaults.RemoteRoot)
}

func TestPreparedRemoteTaskKeepsDirectoryAndNamingSnapshot(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "remote_then_local"}[fallback], func(t *testing.T) {
			pipeline, resources, request := pipelineFixture(t)
			root, remote := filepath.Join(t.TempDir(), localExecutorName), resources.Defaults.RemoteRoot
			executors := []string{testAria2Executor}
			if fallback {
				executors = append(executors, localExecutorName)
			}
			route := &fixedDownloadRoute{ports.DownloadRoute{Executors: executors, LocalRoot: root}}
			resources.Routing = route
			naming := &changingNaming{resources.Naming}
			resources.Naming = naming
			newNaming := namingWatcher(t, "new-F", 255).opts.Naming
			resources.Source.(*batchSourceFixture).register = func() {
				require.NoDirExists(t, root)
				naming.NamingRules = newNaming
				route.route.LocalRoot = filepath.Join(t.TempDir(), "new-local")
				route.route.Executors[0] = "http"
				resources.Defaults.RemoteRoot = filepath.Join(t.TempDir(), "new-remote")
			}
			remoteCalls, localCalls := 0, 0
			resources.Executors[testAria2Executor] = preparedExecutor{name: testAria2Executor, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				remoteCalls++
				require.Equal(t, remote, in.Dir)
				require.Equal(t, testVideoFile, in.Out)
				if fallback {
					return types.DownloadResult{}, ports.ErrDownloadNotAccepted
				}
				return types.DownloadResult{Target: testAria2Executor, ID: in.TaskID}, nil
			}}
			resources.Executors[localExecutorName] = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				localCalls++
				require.Equal(t, root, in.Dir)
				require.Equal(t, testVideoFile, in.Out)
				return types.DownloadResult{Target: localExecutorName, ID: in.TaskID}, nil
			}}
			result, err := pipeline.SubmitBatch(context.Background(), request, resources)
			require.NoError(t, err)
			require.Equal(t, 1, result.Queued)
			require.Equal(t, 1, remoteCalls)
			require.Equal(t, map[bool]int{true: 1, false: 0}[fallback], localCalls)
			require.NoDirExists(t, remote)
			require.NoDirExists(t, resources.Defaults.RemoteRoot)
		})
	}
}

func TestPreparedRemoteNamingFailureStillAllowsLocalFallback(t *testing.T) {
	pipeline, resources, request := pipelineFixture(t)
	root := filepath.Join(t.TempDir(), localExecutorName)
	resources.Routing = &fixedDownloadRoute{ports.DownloadRoute{Executors: []string{testAria2Executor, localExecutorName}, LocalRoot: root}}
	resources.Naming = rejectRemoteNaming{NamingRules: resources.Naming, root: resources.Defaults.RemoteRoot}
	resources.Executors[testAria2Executor] = preparedExecutor{name: testAria2Executor, submit: func(context.Context, types.DownloadSubmission) (types.DownloadResult, error) {
		t.Error("invalid remote target reached RPC")
		return types.DownloadResult{}, nil
	}}
	localCalls := 0
	resources.Executors[localExecutorName] = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
		localCalls++
		require.Equal(t, root, in.Dir)
		return types.DownloadResult{Target: localExecutorName, ID: in.TaskID}, nil
	}}
	result, err := pipeline.SubmitBatch(context.Background(), request, resources)
	require.NoError(t, err)
	require.Equal(t, 1, result.Queued)
	require.Equal(t, 1, localCalls)
	require.NoDirExists(t, resources.Defaults.RemoteRoot)
}

func TestRoutedFallbackUsesIndependentLocalPathOnlyAfterDefiniteRejection(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			pipeline, resources, request := pipelineFixture(t)
			root := filepath.Join(t.TempDir(), localExecutorName)
			resources.Routing = &fixedDownloadRoute{ports.DownloadRoute{Executors: []string{testAria2Executor, localExecutorName}, LocalRoot: root}}
			remoteCalls, localCalls := 0, 0
			resources.Executors[testAria2Executor] = preparedExecutor{name: testAria2Executor, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				remoteCalls++
				require.NotEqual(t, root, in.Dir)
				if ambiguous {
					return types.DownloadResult{}, errors.New("reply lost")
				}
				return types.DownloadResult{}, ports.ErrDownloadNotAccepted
			}}
			resources.Executors[localExecutorName] = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				localCalls++
				require.Equal(t, filepath.Join(root, testVideoFile), in.FullPath)
				return types.DownloadResult{Target: localExecutorName, ID: in.TaskID}, nil
			}}
			result, err := pipeline.SubmitBatch(context.Background(), request, resources)
			require.Equal(t, 1, remoteCalls)
			if ambiguous {
				require.Error(t, err)
				require.Zero(t, localCalls)
				require.Equal(t, 1, result.Uncertain)
				require.Zero(t, result.Queued)
				require.NoDirExists(t, root)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, result.Queued)
				require.Equal(t, 1, localCalls)
				require.DirExists(t, root)
			}
			require.NoDirExists(t, resources.Defaults.RemoteRoot)
		})
	}
}
