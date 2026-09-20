// Package configuration owns the complete user configuration and its persistence.
// Business schemas are collected from the catalog, so adding a declared setting
// also adds it to validation, migration and the generated tdl_config.json.
package configuration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID       = "configuration.manager"
	Filename = "tdl_config.json"
	Version  = 1
)

var namespaceName = regexp.MustCompile(`^[A-Za-z]+$`)

type Component struct {
	Enabled bool           `json:"enabled"`
	Values  map[string]any `json:"values"`
}

type Document struct {
	Version    int                       `json:"version"`
	System     ports.SystemConfiguration `json:"system"`
	Components map[string]Component      `json:"components"`
}

type Service struct {
	mu      sync.Mutex
	file    ports.ConfigurationFile
	catalog *rte.Catalog
}

func New(file ports.ConfigurationFile, catalog *rte.Catalog) *Service {
	return &Service{file: file, catalog: catalog}
}

func Manifest() manifest.Manifest {
	return manifest.Manifest{ID: ID, Title: "统一配置管理", Feature: manifest.Feature{
		ID: "panel", Title: "面板与数据维护", Order: 70, SettingsURL: "/config?tab=system",
	}, Provides: []manifest.Port{manifest.PortOf[ports.ConfigurationManager](ports.ConfigurationManagerName, 1, 0)}}
}

func Register(registry *rte.Registry, service *Service) error {
	return registry.Register(Manifest(), func() rte.Component { return service })
}

func (s *Service) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.ConfigurationManagerName, s)
}
func (s *Service) Start(ctx context.Context) error              { _, err := s.System(ctx); return err }
func (*Service) Stop(context.Context) error                     { return nil }
func (*Service) Reconfigure(context.Context, config.View) error { return nil }

// Complete validates global settings, including disabled components, and fills
// omitted settings from the catalog. Unknown fields are errors, never discarded.
func (s *Service) Complete(ctx context.Context, input Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if input.Version != Version {
		return Document{}, fmt.Errorf("unsupported tdl configuration version %d", input.Version)
	}
	if !namespaceName.MatchString(input.System.Namespace) {
		return Document{}, fmt.Errorf("system.namespace must contain English letters only")
	}
	result := Document{Version: Version, System: input.System, Components: map[string]Component{}}
	for _, definition := range s.catalog.Definitions() {
		id := definition.Manifest.ID
		if id == ID {
			continue
		}
		component, exists := input.Components[id]
		if !exists {
			component.Enabled = id != "trigger.forward"
		}
		view, err := s.catalog.View(ctx, id, component.Values)
		if err != nil {
			return Document{}, fmt.Errorf("components: %w", err)
		}
		component.Values = view.Values()
		result.Components[id] = component
	}
	for id := range input.Components {
		if _, ok := result.Components[id]; !ok {
			return Document{}, fmt.Errorf("components: unknown component %s", id)
		}
	}
	return result, nil
}

func (s *Service) Defaults(ctx context.Context) (Document, error) {
	return s.Complete(ctx, Document{Version: Version, System: ports.SystemConfiguration{Namespace: "default"}})
}

func (s *Service) read(ctx context.Context) (Document, error) {
	data, err := s.file.Read(ctx)
	if err != nil {
		return Document{}, err
	}
	if err := validateJSON(data); err != nil {
		return Document{}, fmt.Errorf("decode %s: %w", Filename, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode %s: %w", Filename, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Document{}, fmt.Errorf("expected one configuration object")
	}
	return s.Complete(ctx, doc)
}

func (s *Service) write(ctx context.Context, doc Document, create bool) error {
	doc, err := s.Complete(ctx, doc)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return s.file.Write(ctx, append(data, '\n'), create)
}

// Create is exclusively used for first startup/import and refuses replacement.
func (s *Service) Create(ctx context.Context, doc Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(ctx, doc, true)
}

func (s *Service) System(ctx context.Context) (ports.SystemConfiguration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.read(ctx)
	return doc.System, err
}

func (s *Service) SetSystem(ctx context.Context, expected, next ports.SystemConfiguration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.read(ctx)
	if err != nil {
		return err
	}
	if doc.System != expected {
		return ports.ErrConfigurationConflict
	}
	doc.System = next
	return s.write(ctx, doc, false)
}

// Store exposes the global component configuration to RTE hosts.
func (s *Service) Store() *config.Store {
	return config.NewManaged(s)
}

func (s *Service) component(ctx context.Context, id string) (Document, Component, error) {
	doc, err := s.read(ctx)
	if err != nil {
		return Document{}, Component{}, err
	}
	if id == ID {
		return doc, Component{Enabled: true, Values: map[string]any{}}, nil
	}
	component, ok := doc.Components[id]
	if !ok {
		return Document{}, Component{}, fmt.Errorf("unknown component %s", id)
	}
	return doc, component, nil
}

func (s *Service) Load(ctx context.Context, id string) (config.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, component, err := s.component(ctx, id)
	return config.Document{Version: config.CurrentVersion, Enabled: component.Enabled, Values: component.Values}, err
}

func (s *Service) Revision(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, component, err := s.component(ctx, id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(component)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (s *Service) Save(ctx context.Context, id string, enabled bool, view config.View) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == ID {
		return fmt.Errorf("configuration manager is always enabled; edit system settings through the system configuration port")
	}
	doc, _, err := s.component(ctx, id)
	if err != nil {
		return err
	}
	doc.Components[id] = Component{Enabled: enabled, Values: view.Values()}
	return s.write(ctx, doc, false)
}
