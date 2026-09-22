package dem

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

func TestBoundedConcurrentHistory(t *testing.T) {
	store := New(types.DefaultAccount, 8)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); store.Report("component", "operation", errors.New("failed")) }()
	}
	wg.Wait()
	store.Report("ignored", "ignored", nil)
	events := store.Events()
	require.Len(t, events, 8)
	for i, event := range events {
		require.Equal(t, uint64(93+i), event.Sequence)
		require.Equal(t, types.DefaultAccount, event.Account)
	}
	events[0].Message = "changed"
	require.Equal(t, "failed", store.Events()[0].Message)
}
