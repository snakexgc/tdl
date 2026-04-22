package login

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/storage/keygen"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/tclient"
)

func TestCommitTemporarySessionCopiesSessionAndApp(t *testing.T) {
	ctx := context.Background()
	tmp := newMemoryStorage()
	dst := newMemoryStorage()

	require.NoError(t, tmp.Set(ctx, keygen.New("session"), []byte("temporary-session")))
	require.NoError(t, commitTemporarySession(ctx, tmp, dst))

	session, err := dst.Get(ctx, keygen.New("session"))
	require.NoError(t, err)
	require.Equal(t, []byte("temporary-session"), session)

	app, err := dst.Get(ctx, key.App())
	require.NoError(t, err)
	require.Equal(t, []byte(tclient.AppDesktop), app)
}

func TestCommitTemporarySessionDoesNotWriteWithoutSession(t *testing.T) {
	ctx := context.Background()
	tmp := newMemoryStorage()
	dst := newMemoryStorage()

	err := commitTemporarySession(ctx, tmp, dst)
	require.Error(t, err)

	_, getErr := dst.Get(ctx, key.App())
	require.ErrorIs(t, getErr, storage.ErrNotFound)
}

func TestValidateAuthenticatedUserRejectsEmptyUser(t *testing.T) {
	require.Error(t, validateAuthenticatedUser(nil))
	require.Error(t, validateAuthenticatedUser(&tg.User{}))
	require.NoError(t, validateAuthenticatedUser(&tg.User{ID: 42}))
}

func TestUserSummaryMarksEmptyUserInvalid(t *testing.T) {
	require.Equal(t, "ID: (invalid), Username: (not set), Name: (not set)", UserSummary(&tg.User{}))
}

func TestMemoryStorageCopiesValues(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStorage()
	original := []byte("value")

	require.NoError(t, store.Set(ctx, "k", original))
	original[0] = 'X'

	got, err := store.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), got)

	got[0] = 'Y'
	got, err = store.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), got)
}
