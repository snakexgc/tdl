package panel

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/login", Public: true},
		{Path: "/api/auth/session", Public: true},
		{Path: "/api/auth/login", Public: true},
		{Path: "/api/auth/logout", Public: false},
		{Path: "/views/", Public: false},
		{Path: "/components.html", Public: false},
		{Path: "/api/heartbeat", Public: false},
		{Path: "/api/events", Public: false},
		{Path: "/api/dashboard", Public: false},
		{Path: "/api/status", Public: false},
		{Path: "/api/kv/links", Public: false},
		{Path: "/api/kv/links/actions", Public: false},
		{Path: "/api/kv/links/", Public: false},
		{Path: "/api/modules", Public: false},
		{Path: "/api/components", Public: false},
		{Path: "/api/components/health", Public: false},
		{Path: "/api/config", Public: false},
		{Path: "/api/system/reboot", Public: false},
		{Path: "/", Public: false},
	}
}
