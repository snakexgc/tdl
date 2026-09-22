package updater

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/api/update/check", Public: false},
		{Path: "/api/update/apply", Public: false},
	}
}
