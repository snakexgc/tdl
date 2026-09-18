package aria2

import (
	"context"
	"errors"
	"net"
	"sync"
)

const testDownloadDir = "downloads"

const testGID1 = "gid-1"

type testRepository struct {
	mu      sync.Mutex
	records map[string]TaskRecord
}

func newTestRepository() *testRepository { return &testRepository{records: map[string]TaskRecord{}} }
func (r *testRepository) Add(_ context.Context, v TaskRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[v.GID] = v
	return nil
}

func (r *testRepository) Remove(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, id)
	return nil
}

func (r *testRepository) Report(_ context.Context, record TaskRecord, revision uint64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.records[record.GID]
	if !exists || current.Revision != revision {
		return false, nil
	}
	record.Revision++
	r.records[record.GID] = record
	return true, nil
}

func (r *testRepository) Records(context.Context) (map[string]TaskRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]TaskRecord{}
	for id, v := range r.records {
		out[id] = v
	}
	return out, nil
}

func (r *testRepository) GIDs(ctx context.Context) (map[string]struct{}, error) {
	records, err := r.Records(ctx)
	out := map[string]struct{}{}
	for id := range records {
		out[id] = struct{}{}
	}
	return out, err
}

func fakeAria2ConnectionError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}
