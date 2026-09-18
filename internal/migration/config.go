// Package migration converts supported legacy settings without changing the source.
package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/componentconfig"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

type Plan struct {
	Account    types.AccountID `json:"account"`
	Components []string        `json:"components"`
	// Values never appear in a preview or log: they can contain credentials.
	documents map[string]config.Document
}

func Prepare(reader io.Reader) (*Plan, error) {
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("legacy configuration exceeds size limit")
	}
	type rawConfig legacy.Config
	strict := struct {
		*rawConfig
		LegacyMinimum json.RawMessage `json:"file_size_mb"`
	}{rawConfig: new(rawConfig)}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&strict); err != nil {
		return nil, fmt.Errorf("decode legacy configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("expected one legacy configuration object")
	}
	cfg := legacy.DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if err := legacy.Validate(cfg); err != nil {
		return nil, err
	}
	catalog, err := application.Catalog()
	if err != nil {
		return nil, err
	}
	documents, err := componentconfig.Export(cfg, catalog)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Account: types.AccountID(cfg.Namespace), documents: documents}
	for id := range documents {
		plan.Components = append(plan.Components, id)
	}
	sort.Strings(plan.Components)
	return plan, nil
}

func (p *Plan) Validate(ctx context.Context) error {
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	for _, id := range p.Components {
		if _, err := catalog.View(ctx, id, p.documents[id].Values); err != nil {
			return err
		}
	}
	return nil
}

// Write refuses existing destinations, even empty directories. It exports to an
// exclusively created directory; on failure it removes only files it created.
// A completion marker is written last so incomplete output is identifiable.
func (p *Plan) Write(ctx context.Context, destination string) error {
	if err := p.Validate(ctx); err != nil {
		return err
	}
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return fmt.Errorf("create new migration directory: %w", err)
	}
	// This directory was created exclusively above. Do not clean up a path
	// selected from source configuration or traverse an existing user directory.
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(target)
		}
	}()
	store := config.NewStore(target)
	for _, id := range p.Components {
		document := p.documents[id]
		view, err := catalog.View(ctx, id, document.Values)
		if err != nil {
			return err
		}
		if err := store.Save(ctx, id, document.Enabled, view); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(target, "migration.json"), report, 0o600); err != nil {
		return err
	}
	complete = true
	return nil
}
