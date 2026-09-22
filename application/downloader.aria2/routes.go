package aria2

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/aria2ng.html", Public: false},
		{Path: "/aria2/jsonrpc", Public: false},
		{Path: "/api/aria2/check", Public: false},
	}
}
