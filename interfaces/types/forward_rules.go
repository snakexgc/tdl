package types

// ChatRef is unambiguous across Telegram users, basic groups and channels.
type ChatRef string

type ForwardRule struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`
	Sources []ChatRef `json:"sources"`
	Targets []ChatRef `json:"targets"`
	Mode    string    `json:"mode"`
	Silent  bool      `json:"silent"`
}

type ForwardDestination struct {
	RuleID string
	Target ChatRef
	Mode   string
	Silent bool
	Name   string
}

type Dialog struct {
	Ref      ChatRef `json:"ref"`
	Title    string  `json:"title"`
	Username string  `json:"username,omitempty"`
	Kind     string  `json:"kind"`
}
