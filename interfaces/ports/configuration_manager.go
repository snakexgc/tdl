package ports

import "context"

const ConfigurationManagerName = "configuration.manager"

// ConfigurationFile keeps filesystem access below the configuration policy SWC.
type ConfigurationFile interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte, bool) error
}

type SystemConfiguration struct {
	Namespace string `json:"namespace"`
	Debug     bool   `json:"debug"`
}

type ConfigurationManager interface {
	System(context.Context) (SystemConfiguration, error)
	SetSystem(context.Context, SystemConfiguration, SystemConfiguration) error
}
