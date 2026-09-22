package downloadcontrol

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type linkRemovalBackend struct {
	backendStub
	fail bool
}

func (b *linkRemovalBackend) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	if b.fail {
		return types.DownloadActionResult{Errors: []string{"worker still active"}}, nil
	}
	return b.backendStub.ChangeTasks(ctx, action, ids)
}

type linkRemovalRepository struct {
	t       *testing.T
	backend *linkRemovalBackend
	calls   int
}

func (r *linkRemovalRepository) Remove(_ context.Context, id string) (int, error) {
	require.Equal(r.t, actionDelete, r.backend.action)
	require.Equal(r.t, []string{id}, r.backend.ids)
	r.calls++
	return 2, nil
}

func TestLinkRemovalRequiresSuccessfulLocalDeletion(t *testing.T) {
	backend := &linkRemovalBackend{fail: true}
	repo := &linkRemovalRepository{t: t, backend: backend}
	control := NewLinkControl("bound", repo, backend)
	_, err := control.Remove(context.Background(), "other", "source")
	require.ErrorContains(t, err, "account mismatch")
	_, err = control.Remove(context.Background(), "bound", "index")
	require.Error(t, err)
	_, err = control.Remove(context.Background(), "bound", "source")
	require.ErrorContains(t, err, "worker still active")
	require.Zero(t, repo.calls)
	backend.fail = false
	count, err := control.Remove(context.Background(), "bound", "source")
	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.Equal(t, 1, repo.calls)
}
