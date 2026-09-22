package types

import "time"

// DiagnosticEvent contains control-plane failures, never business payloads.
type DiagnosticEvent struct {
	Sequence  uint64    `json:"sequence"`
	Account   AccountID `json:"account"`
	Component string    `json:"component"`
	Operation string    `json:"operation"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}
