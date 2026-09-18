package accounttelegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

const (
	testCatalogAlice = "alice"
	testCatalogBob   = "bob"
)

type catalogRepository struct{ deleted string }

func (*catalogRepository) List(context.Context) ([]string, error) {
	return []string{testCatalogBob, testCatalogAlice, testCatalogAlice, "../bad", ""}, nil
}

func (r *catalogRepository) Delete(_ context.Context, name string) (int, error) {
	r.deleted = name
	return 3, nil
}

func TestSessionCatalogProtectsCurrentAndInvalidNames(t *testing.T) {
	ctx := context.Background()
	repository := &catalogRepository{}
	service := NewSessions(testCatalogBob, repository)
	items, err := service.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []ports.SessionOption{{Namespace: testCatalogBob, Current: true}, {Namespace: testCatalogAlice}}, items)
	for _, name := range []string{testCatalogBob, "../bad", ""} {
		_, err := service.Delete(ctx, name)
		require.Error(t, err)
	}
	require.Empty(t, repository.deleted)
	count, err := service.Delete(ctx, testCatalogAlice)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.Equal(t, testCatalogAlice, repository.deleted)
}
