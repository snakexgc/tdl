package types

type LoginStatus struct {
	Active    bool           `json:"active"`
	Kind      string         `json:"kind,omitempty"`
	Stage     string         `json:"stage,omitempty"`
	Status    string         `json:"status,omitempty"`
	Error     string         `json:"error,omitempty"`
	Phone     string         `json:"phone,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	User      map[string]any `json:"user,omitempty"`
}
