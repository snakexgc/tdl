package types

import "time"

type TimeSample struct {
	Offset  time.Duration
	Elapsed time.Duration
}

type ClockStatus struct {
	Server       string        `json:"server"`
	Synchronized bool          `json:"synchronized"`
	Offset       time.Duration `json:"offset_ns"`
	LastAttempt  time.Time     `json:"last_attempt"`
	LastSuccess  time.Time     `json:"last_success"`
	LastError    string        `json:"last_error,omitempty"`
}
