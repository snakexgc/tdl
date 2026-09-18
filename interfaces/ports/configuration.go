package ports

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/snakexgc/tdl/interfaces/types"
)

const ConfigurationName = "panel.configuration"

var ErrConfigurationConflict = errors.New("configuration changed; reload before saving")

type Configuration interface {
	Read(context.Context) (*types.RuntimeConfig, error)
	Patch(context.Context, map[string]json.RawMessage) (*types.RuntimeConfig, error)
}

type ConfigurationStore interface {
	Read(context.Context) (*types.RuntimeConfig, error)
	Validate(*types.RuntimeConfig) error
	Save(context.Context, *types.RuntimeConfig, *types.RuntimeConfig) error
}
