package aria2

import (
	"sync/atomic"
	"time"
)

type linkPolicy struct {
	baseURL string
	ttl     time.Duration
}

func currentBaseURL(policy *atomic.Pointer[linkPolicy], fallback string) string {
	if policy != nil {
		if current := policy.Load(); current != nil {
			return current.baseURL
		}
	}
	return fallback
}

// UpdateLinkPolicy publishes link ownership and retention metadata without
// interrupting the RPC connection or pausing any transfer.
func (m *Manager) UpdateLinkPolicy(baseURL string, ttl time.Duration) {
	m.links.Store(&linkPolicy{baseURL: baseURL, ttl: ttl})
}
