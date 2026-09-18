package accounttelegram

import "github.com/snakexgc/tdl/interfaces/types"

// Routes declares the component-owned HTTP control surface.
func Routes() []types.WebRoute {
	return []types.WebRoute{
		{Path: "/api/user", Public: false},
		{Path: "/api/user/switch", Public: false},
		{Path: "/api/user/delete", Public: false},
		{Path: "/api/user/spam-check", Public: false},
		{Path: "/api/login/status", Public: false},
		{Path: "/api/login/phone/start", Public: false},
		{Path: "/api/login/code", Public: false},
		{Path: "/api/login/password", Public: false},
		{Path: "/api/login/cancel", Public: false},
	}
}
