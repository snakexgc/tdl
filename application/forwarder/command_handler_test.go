package forwarder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const commandTestLink = "https://t.me/example/1"

func commandTestValidator(ctx context.Context, _ types.AccountID, link string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !strings.HasPrefix(link, "https://t.me/example/") {
		return "", errors.New("invalid message link")
	}
	return link, nil
}

func commandTestRequest() types.ConsoleRequest {
	return types.ConsoleRequest{Account: types.DefaultAccount, Name: forwardCommandName, Text: "/forward", ReplyText: commandTestLink, Private: true}
}

func TestForwardCommandUsesLiveComponentPolicyAndRejectsStoppedHandle(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	build := func(target string) (*rte.Runtime, ports.ConsoleCommandHandler) {
		registry := rte.NewRegistry()
		require.NoError(t, Register(registry, q, transportFunc(func(ctx context.Context, _ *types.ForwardJob, _ func(types.ForwardJob)) error {
			<-ctx.Done()
			return ctx.Err()
		}), nil, Options{Validate: commandTestValidator}))
		host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {"target": target}})
		require.NoError(t, err)
		require.Equal(t, rte.Running, host.Start(ctx)[0].State)
		t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
		value, err := host.Resolve(commandPort)
		require.NoError(t, err)
		return host, value.(ports.ConsoleCommandHandler)
	}
	host, handler := build("@first")
	request := commandTestRequest()
	request.ReplyText = commandTestLink + ",\n" + commandTestLink + " https://example.com/invalid"
	response, err := handler.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, response.Text, "1 条")
	store := config.NewStore(t.TempDir())
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{"target": "@second", "mode": forwardModeClone, "silent": true}, store))
	request.ReplyText = "https://t.me/example/2"
	_, err = handler.Execute(ctx, request)
	require.NoError(t, err)
	request.Text, request.ReplyText = "/forward@mybot @explicit", "https://t.me/example/3"
	_, err = handler.Execute(ctx, request)
	require.NoError(t, err)
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	for _, job := range jobs {
		switch job.SourceLink {
		case commandTestLink:
			require.Equal(t, "@first", job.Destination)
			require.Equal(t, forwardModeDefault, job.Mode)
			require.False(t, job.Silent)
		case "https://t.me/example/2":
			require.Equal(t, "@second", job.Destination)
			require.Equal(t, forwardModeClone, job.Mode)
			require.True(t, job.Silent)
		default:
			require.Equal(t, "@explicit", job.Destination)
			require.Equal(t, forwardModeClone, job.Mode)
		}
	}
	require.NoError(t, host.Stop(ctx))
	_, err = handler.Execute(ctx, request)
	require.ErrorContains(t, err, "stopped")
	_, replacement := build("@replacement")
	_, err = handler.Execute(ctx, request)
	require.ErrorContains(t, err, "stopped")
	_, err = replacement.Execute(ctx, commandTestRequest())
	require.NoError(t, err)
}

func TestForwardCommandChecksAccountPrivacyAndCancellation(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	handler := NewCommand(ctx, types.DefaultAccount, q, commandTestValidator, func() CommandSettings { return CommandSettings{Mode: forwardModeDefault} })
	defer handler.Stop(ctx)
	request := commandTestRequest()
	request.Account = "another"
	_, err := handler.Execute(ctx, request)
	require.ErrorContains(t, err, "account mismatch")
	request.Account, request.Private = types.DefaultAccount, false
	response, err := handler.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, response.Text, "私聊")
	request.Private, request.Text = true, "/forward too many"
	response, err = handler.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, response.Text, "用法")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = handler.Execute(canceled, commandTestRequest())
	require.ErrorIs(t, err, context.Canceled)
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestForwardCommandStopCancelsAndWaitsForActiveRequest(t *testing.T) {
	ctx := context.Background()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := NewCommand(ctx, types.DefaultAccount, newTestQueue(), func(ctx context.Context, _ types.AccountID, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return "", ctx.Err()
	}, func() CommandSettings { return CommandSettings{} })
	result := make(chan error, 1)
	go func() { _, err := handler.Execute(ctx, commandTestRequest()); result <- err }()
	defer func() {
		close(release)
		require.NoError(t, handler.Stop(ctx))
		require.ErrorIs(t, <-result, context.Canceled)
	}()
	<-entered
	stop, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, handler.Stop(stop), context.DeadlineExceeded)
	<-canceled
	_, err := handler.Execute(ctx, commandTestRequest())
	require.ErrorContains(t, err, "stopped")
}

type partialCommandRepository struct {
	memoryRepository
	calls int
}

func (s *partialCommandRepository) Save(ctx context.Context, job Job) error {
	s.calls++
	if s.calls == 2 {
		return errors.New("storage full")
	}
	return s.memoryRepository.Save(ctx, job)
}

func TestForwardCommandReportsPartialAdmissionAndWakesWorker(t *testing.T) {
	ctx := context.Background()
	q := NewQueue(&partialCommandRepository{memoryRepository: memoryRepository{jobs: map[string]Job{}}})
	handler := NewCommand(ctx, types.DefaultAccount, q, commandTestValidator, func() CommandSettings { return CommandSettings{} })
	defer handler.Stop(ctx)
	request := commandTestRequest()
	request.ReplyText += " https://t.me/example/2"
	response, err := handler.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, response.Text, "1 条")
	require.Contains(t, response.Text, "storage full")
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	select {
	case <-q.wake:
	default:
		t.Fatal("accepted work was not signaled after partial admission")
	}
}
