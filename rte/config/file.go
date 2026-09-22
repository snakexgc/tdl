package config

import (
	"context"

	"github.com/snakexgc/tdl/bsw/services/configfile"
)

// File is the basic-software adapter injected into the configuration SWC.
type File struct{ Path string }

func (f File) Read(ctx context.Context) ([]byte, error) { return configfile.Read(ctx, f.Path) }
func (f File) Write(ctx context.Context, data []byte, create bool) error {
	return configfile.Write(ctx, f.Path, data, create)
}
