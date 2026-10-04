package types

import "time"

const (
	ClockSourceSystem = "system"
	ClockSourceNTP    = "ntp"
	ClockSourceHTTPS  = "https"
)

type TimeSample struct {
	Offset     time.Duration
	Elapsed    time.Duration
	MeasuredAt time.Time // Local time, including its monotonic reading, at measurement.
	Source     string
	Server     string
}

type ClockStatus struct {
	Server       string        `json:"server"`
	Source       string        `json:"source"`
	ViaProxy     bool          `json:"via_proxy"`
	Synchronized bool          `json:"synchronized"`
	Offset       time.Duration `json:"offset_ns"`
	LastAttempt  time.Time     `json:"last_attempt"`
	LastSuccess  time.Time     `json:"last_success"`
	LastError    string        `json:"last_error,omitempty"`
}
