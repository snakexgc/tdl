package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAria2TaskStoreKeepsRecordWhenTTLDisabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newAria2TaskStore(newMemoryTaskStorage(), 0)
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         testGID1,
		TaskID:      testDocument1,
		DownloadURL: testDownloadURL1,
		CreatedAt:   time.Now().Add(-DefaultTaskTTL - time.Second),
	}))

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.Contains(t, records, testGID1)
}
