package watch

import (
	"sync"
	"time"
)

// speedWindowDuration matches aria2's WINDOW_TIME constant: the sliding window
// over which download speed is averaged.
const speedWindowDuration = 10 * time.Second

type speedSlot struct {
	t     time.Time
	bytes int64
}

// speedCalc tracks download throughput using a 10-second sliding window,
// mirroring aria2's SpeedCalc algorithm. Bytes received within the same
// wall-clock second are coalesced into one slot; speed is calculated as
// total_bytes_in_window * 1s / elapsed_since_oldest_slot.
type speedCalc struct {
	mu    sync.Mutex
	slots []speedSlot
}

// update records n bytes as received now.
func (s *speedCalc) update(n int64) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// Aggregate into the current-second slot when possible.
	if len(s.slots) > 0 && now.Sub(s.slots[len(s.slots)-1].t) < time.Second {
		s.slots[len(s.slots)-1].bytes += n
	} else {
		s.slots = append(s.slots, speedSlot{t: now, bytes: n})
	}
	s.prune(now)
}

// speed returns the current download speed in bytes per second.
// Returns 0 when no data has been received within the window.
func (s *speedCalc) speed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.prune(now)
	if len(s.slots) == 0 {
		return 0
	}
	var total int64
	for _, slot := range s.slots {
		total += slot.bytes
	}
	elapsed := now.Sub(s.slots[0].t)
	if elapsed < time.Millisecond {
		return 0
	}
	return total * int64(time.Second) / int64(elapsed)
}

// prune removes slots that have fallen outside the sliding window.
// Must be called with s.mu held.
func (s *speedCalc) prune(now time.Time) {
	cutoff := now.Add(-speedWindowDuration)
	i := 0
	for i < len(s.slots) && s.slots[i].t.Before(cutoff) {
		i++
	}
	if i > 0 {
		s.slots = s.slots[i:]
	}
}
