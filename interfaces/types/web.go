package types

// WebRoute declares ownership and authentication of an HTTP control endpoint.
// The HTTP adapter must bind every route and reject undeclared handlers.
type WebRoute struct {
	Path   string
	Public bool
}
