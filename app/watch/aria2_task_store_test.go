package watch

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
		GID:         "gid-1",
		TaskID:      "document_1",
		DownloadURL: "http://127.0.0.1:8080/download/document_1",
		CreatedAt:   time.Now().Add(-defaultDownloadTaskTTL - time.Second),
	}))

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.Contains(t, records, "gid-1")
}
