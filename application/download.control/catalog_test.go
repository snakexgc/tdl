package downloadcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	catalogAccount = "catalog-account"
	catalogFirst   = "first"
	catalogSecond  = "second"
)

type catalogSourceStub struct {
	resources ports.LinkSubmissionResources
	snapshot  types.LinkCatalogSnapshot
	reads     int
	marked    []string
	markErr   error
}

func (s *catalogSourceStub) Observe(context.Context) (types.LinkCatalogSnapshot, error) {
	s.reads++
	return s.snapshot, nil
}

func (s *catalogSourceStub) Submission(context.Context) (ports.LinkSubmissionResources, error) {
	s.reads++
	return s.resources, nil
}

func (s *catalogSourceStub) MarkDownloaded(_ context.Context, id string) error {
	s.marked = append(s.marked, id)
	return s.markErr
}

type catalogRemoteStub struct {
	ports.Aria2Client
	configured int
	added      int
	cancel     context.CancelFunc
}

func (r *catalogRemoteStub) SetMaxConcurrentDownloads(context.Context, int) error {
	r.configured++
	return nil
}

func (r *catalogRemoteStub) AddURI(context.Context, string, types.Aria2AddURIOptions) (string, error) {
	r.added++
	if r.cancel != nil {
		r.cancel()
	}
	return "accepted-gid", nil
}

type catalogRepositoryStub struct {
	ports.Aria2Repository
	records []types.Aria2TaskRecord
	err     error
}

func (r *catalogRepositoryStub) Add(_ context.Context, record types.Aria2TaskRecord) error {
	r.records = append(r.records, record)
	return r.err
}

func TestCatalogRejectsAccountAndCancellationBeforeAccess(t *testing.T) {
	source := &catalogSourceStub{}
	catalog := NewCatalog(catalogAccount, source)
	_, _, err := catalog.List(context.Background(), "foreign")
	require.Error(t, err)
	require.False(t, catalog.Submit(context.Background(), "foreign", []string{catalogFirst}).OK)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = catalog.List(ctx, catalogAccount)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, catalog.Submit(ctx, catalogAccount, []string{catalogFirst}).OK)
	require.Zero(t, source.reads)
}

func TestCatalogSubmissionDeduplicatesAndReportsAcceptedWork(t *testing.T) {
	remote := &catalogRemoteStub{}
	repository := &catalogRepositoryStub{err: errors.New("disk unavailable")}
	source := &catalogSourceStub{resources: ports.LinkSubmissionResources{
		Mode: aria2Executor, Remote: remote, Repository: repository, Limit: 2, Connections: 4,
		Records: map[string][]byte{catalogFirst: []byte(`{"file_name":"file.bin"}`), "mismatch": []byte(`{"id":"another"}`)},
	}}
	result := NewCatalog(catalogAccount, source).Submit(context.Background(), catalogAccount, []string{catalogFirst, catalogFirst, "index", "mismatch"})
	require.False(t, result.OK)
	require.Equal(t, 1, result.Added)
	require.Equal(t, 3, result.Skipped)
	require.Equal(t, 1, remote.added)
	require.Equal(t, 1, remote.configured)
	require.Contains(t, result.Errors[0], "accepted-gid")
	require.Contains(t, result.Errors[0], "disk unavailable")
	require.Equal(t, catalogFirst, repository.records[0].TaskID)
}

func TestCatalogCancellationStopsFurtherSubmissions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := &catalogRemoteStub{cancel: cancel}
	source := &catalogSourceStub{resources: ports.LinkSubmissionResources{Mode: aria2Executor, Remote: remote, Repository: &catalogRepositoryStub{}, Records: map[string][]byte{catalogFirst: []byte(`{}`), catalogSecond: []byte(`{}`)}}}
	result := NewCatalog(catalogAccount, source).Submit(ctx, catalogAccount, []string{catalogFirst, catalogSecond})
	require.False(t, result.OK)
	require.Equal(t, 1, remote.added)
	require.Equal(t, 1, result.Added)
}

func TestCatalogProjectionPreservesSlidingExpiryAndIncompleteTransfers(t *testing.T) {
	now := time.Now()
	source := &catalogSourceStub{markErr: errors.New("read only"), snapshot: types.LinkCatalogSnapshot{TTL: time.Hour, Records: []types.LinkCatalogRecord{
		{Key: "b", Task: types.PersistentLink{ID: catalogSecond, CreatedAt: now.Add(-2 * time.Hour), LastActiveAt: now}, Aria2: []types.Aria2LinkEntry{{Status: statusComplete, Total: 100, Completed: 99}}},
		{Key: "a", Task: types.PersistentLink{ID: catalogFirst, CreatedAt: now.Add(-2 * time.Hour)}, HTTPCompleted: true, HTTPCompletedAt: now, HTTPDeliveredBytes: 100},
	}}}
	items, statusErr, err := NewCatalog(catalogAccount, source).List(context.Background(), catalogAccount)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, catalogFirst, items[0].ID)
	require.True(t, items[0].Expired)
	require.True(t, items[0].Downloaded)
	require.False(t, items[1].Expired)
	require.False(t, items[1].Downloaded)
	require.Equal(t, []string{catalogFirst}, source.marked)
	require.Contains(t, statusErr, "read only")
}
