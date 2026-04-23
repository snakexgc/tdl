package bot

import "sync"

type allowedUsers struct {
	mu  sync.RWMutex
	ids map[int64]struct{}
}

func newAllowedUsers(ids []int64) *allowedUsers {
	a := &allowedUsers{}
	a.Replace(ids)
	return a
}

func (a *allowedUsers) Contains(id int64) bool {
	if a == nil {
		return false
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.ids[id]
	return ok
}

func (a *allowedUsers) Replace(ids []int64) {
	if a == nil {
		return
	}

	next := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		next[id] = struct{}{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = next
}
