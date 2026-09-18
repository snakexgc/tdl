package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testGID1         = "gid-1"
	testDocument1    = "document_1"
	testDownloadURL1 = "http://127.0.0.1:8080/download/document_1"
)

func TestAria2TaskStoreKeepsRecordWhenTTLDisabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage(), 0)
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
