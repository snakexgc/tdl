package forwarder

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/api/forwards", Public: false},
		{Path: "/api/forwards/actions", Public: false},
	}
}
