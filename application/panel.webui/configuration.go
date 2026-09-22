package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type ConfigurationService struct {
	store ports.ConfigurationStore
	mu    sync.Mutex
}

func NewConfiguration(store ports.ConfigurationStore) *ConfigurationService {
	return &ConfigurationService{store: store}
}

func (s *ConfigurationService) Read(ctx context.Context) (ports.SystemConfiguration, error) {
	return s.store.Read(ctx)
}

func (s *ConfigurationService) Patch(ctx context.Context, values map[string]json.RawMessage) (ports.SystemConfiguration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := s.store.Read(ctx)
	if err != nil {
		return ports.SystemConfiguration{}, err
	}
	next := before
	for key, raw := range values {
		if key != debugSetting {
			return ports.SystemConfiguration{}, fmt.Errorf("unknown system setting %q; edit component settings in the component configuration page", key)
		}
		var debug *bool
		if err := json.Unmarshal(raw, &debug); err != nil {
			return ports.SystemConfiguration{}, fmt.Errorf("debug must be a boolean: %w", err)
		}
		if debug == nil {
			return ports.SystemConfiguration{}, fmt.Errorf("debug must be a boolean")
		}
		next.Debug = *debug
	}
	if err := s.store.Save(ctx, before, next); err != nil {
		return ports.SystemConfiguration{}, err
	}
	return next, nil
}

const (
	debugSetting = "debug"
)
