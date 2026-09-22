package maintenance

import "github.com/snakexgc/tdl/interfaces/types"

func Routes() []types.WebRoute {
	return []types.WebRoute{{Path: "/api/storage/clean", Public: false}}
}
