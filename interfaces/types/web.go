package types

import "encoding/json"

// WebRoute declares ownership and authentication of an HTTP control endpoint.
// The HTTP adapter must bind every route and reject undeclared handlers.
type WebRoute struct {
	Path   string
	Public bool
	Owner  string
	Port   string
}

// WebRequest and WebResponse are JSON control messages, never file streams.
type WebRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   json.RawMessage
}

type WebResponse struct {
	Status int
	Body   any
}
