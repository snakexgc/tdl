package config

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/ports"
)

// SelectNamespace updates only installation selection under the configuration
// lock. A login that began in an old account cannot undo a newer selection or
// overwrite unrelated boot settings saved while authentication was in flight.
func SelectNamespace(ctx context.Context, expected, target string) (bool, error) {
	target, err := NormalizeNamespace(target)
	if err != nil {
		return false, err
	}
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if instance == nil {
		return false, fmt.Errorf("configuration is not initialized")
	}
	if instance.Namespace != expected {
		return false, ports.ErrConfigurationConflict
	}
	if target == expected {
		return false, nil
	}
	next, err := Clone(instance)
	if err != nil {
		return false, err
	}
	next.Namespace = target
	if err := Save(configPath, next); err != nil {
		return false, err
	}
	instance = next
	return true, nil
}
