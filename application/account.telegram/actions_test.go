package accounttelegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type selectionStub struct {
	current  string
	conflict bool
}

func (s *selectionStub) Current(context.Context) (string, error) { return s.current, nil }
func (s *selectionStub) Select(_ context.Context, old, next string) error {
	if s.conflict || old != s.current {
		return errors.New("selection changed")
	}
	s.current = next
	return nil
}

type spamStub func(context.Context, types.AccountID) (string, error)

func (s spamStub) Reply(ctx context.Context, account types.AccountID) (string, error) {
	return s(ctx, account)
}

func TestAccountActionsValidateSelectionAndDoNotOverwriteConcurrentChange(t *testing.T) {
	ctx := context.Background()
	selection := &selectionStub{current: testCatalogBob}
	actions := NewActions(ctx, testCatalogBob, NewSessions(testCatalogBob, &catalogRepository{}), selection, nil)
	defer func() { require.NoError(t, actions.Stop(ctx)) }()
	for _, target := range []string{"../invalid-account", "unknown"} {
		_, err := actions.Switch(ctx, testCatalogBob, target)
		require.Error(t, err)
	}
	_, err := actions.Switch(ctx, testCatalogAlice, testCatalogAlice)
	require.Error(t, err)
	selection.conflict = true
	_, err = actions.Switch(ctx, testCatalogBob, testCatalogAlice)
	require.Error(t, err)
	require.Equal(t, testCatalogBob, selection.current)
	selection.conflict = false
	changed, err := actions.Switch(ctx, testCatalogBob, testCatalogAlice)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = actions.Switch(ctx, testCatalogBob, testCatalogAlice)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestAccountStopCancelsSpamAndRejectsOverlappingOperations(t *testing.T) {
	started := make(chan struct{})
	actions := NewActions(context.Background(), testCatalogBob, nil, nil, spamStub(func(ctx context.Context, _ types.AccountID) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}))
	finished := make(chan error, 1)
	go func() { _, err := actions.CheckSpam(context.Background(), testCatalogBob); finished <- err }()
	<-started
	_, err := actions.Switch(context.Background(), testCatalogBob, testCatalogAlice)
	require.ErrorContains(t, err, "in progress")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, actions.Stop(ctx))
	require.ErrorIs(t, <-finished, context.Canceled)
	_, err = actions.CheckSpam(ctx, testCatalogBob)
	require.ErrorContains(t, err, "stopped")
}
