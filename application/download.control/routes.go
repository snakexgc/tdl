package downloadcontrol

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/api/internal-downloads", Public: false},
		{Path: "/api/download-tasks", Public: false},
		{Path: "/api/download-tasks/actions", Public: false},
		{Path: "/api/internal-downloads/actions", Public: false},
	}
}
