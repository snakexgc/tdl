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
	"strconv"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

type Plan struct {
	Account    types.AccountID `json:"account"`
	Components []string        `json:"components"`
	// Values never appear in a preview or log: they can contain credentials.
	values map[string]map[string]any
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
	stringsValue := func(values []string) []string { return append([]string{}, values...) }
	recipients := make([]string, 0, len(cfg.Bot.AllowedUsers))
	for _, id := range cfg.Bot.AllowedUsers {
		recipients = append(recipients, strconv.FormatInt(id, 10))
	}
	values := map[string]map[string]any{
		"console.bot":         {"allowed_users": recipients},
		"notify.telegram":     {"recipients": recipients},
		"account.telegram":    {"api_id": cfg.Telegram.APIID, "api_hash": cfg.Telegram.APIHash, "builtin_preset": cfg.Telegram.BuiltinPreset, "use_builtin": cfg.Telegram.UseBuiltin},
		"filter.rules":        {"include": stringsValue(cfg.Include), "exclude": stringsValue(cfg.Exclude), "min_mb": cfg.FileSizeMinMB, "max_mb": cfg.FileSizeMaxMB},
		"naming.rules":        {"filename": legacy.EffectiveFilename(cfg), "directory": cfg.DownloadDir, "max_bytes": legacy.EffectiveFilenameMax(cfg)},
		"trigger.reaction":    {"download": stringsValue(cfg.TriggerReactions), "forward": stringsValue(cfg.Forward.TriggerReactions)},
		"trigger.messagelink": {},
		"update.self":         {"proxy": legacy.EffectiveProxy(cfg)},
	}
	plan := &Plan{Account: types.AccountID(cfg.Namespace), values: values}
	for id := range values {
		plan.Components = append(plan.Components, id)
	}
	sort.Strings(plan.Components)
	return plan, nil
}

func (p *Plan) start(ctx context.Context) (*rte.Runtime, error) {
	registry, err := application.Registry()
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(p.Components))
	for _, id := range p.Components {
		enabled[id] = true
	}
	host, err := registry.Build(p.Account, enabled, p.values)
	if err != nil {
		return nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	return host, nil
}

func (p *Plan) Validate(ctx context.Context) error {
	host, err := p.start(ctx)
	if err != nil {
		return err
	}
	return host.Stop(ctx)
}

// Write refuses existing destinations, even empty directories. It exports to an
// exclusively created directory; on failure it removes only files it created.
// A completion marker is written last so incomplete output is identifiable.
func (p *Plan) Write(ctx context.Context, destination string) error {
	host, err := p.start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = host.Stop(context.Background()) }()
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
	if err := host.ExportConfig(ctx, config.NewStore(target)); err != nil {
		return err
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
