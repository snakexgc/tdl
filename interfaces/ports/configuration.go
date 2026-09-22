package ports

import (
	"context"
	"encoding/json"
	"errors"
)

const ConfigurationName = "panel.configuration"

var ErrConfigurationConflict = errors.New("configuration changed; reload before saving")

type Configuration interface {
	Read(context.Context) (SystemConfiguration, error)
	Patch(context.Context, map[string]json.RawMessage) (SystemConfiguration, error)
}

type ConfigurationStore interface {
	Read(context.Context) (SystemConfiguration, error)
	Save(context.Context, SystemConfiguration, SystemConfiguration) error
}
