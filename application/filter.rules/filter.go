package filterrules

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "filter.rules"

const (
	includeField = "include"
	excludeField = "exclude"
	minMBField   = "min_mb"
	maxMBField   = "max_mb"
)

func Register(registry *rte.Registry) error {
	zero := int64(0)
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "下载过滤规则",
		Provides: []manifest.Port{manifest.PortOf[ports.FilterRules](ports.FilterRulesName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: includeField, Title: "允许的扩展名", Type: manifest.Strings, Default: []string{}},
			{Name: excludeField, Title: "排除的扩展名", Type: manifest.Strings, Default: []string{}},
			{Name: minMBField, Title: "最小文件大小（MB）", Type: manifest.Int, Default: 0, Min: &zero},
			{Name: maxMBField, Title: "最大文件大小（MB）", Type: manifest.Int, Default: 0, Min: &zero},
		},
	}, func() rte.Component { return &Rules{} })
}

type settings struct {
	include, exclude map[string]struct{}
	min, max         int64
}
type Rules struct{ settings atomic.Pointer[settings] }

func (r *Rules) Init(ctx context.Context, kernel rte.Kernel) error {
	if err := r.Reconfigure(ctx, kernel.Config); err != nil {
		return err
	}
	return kernel.Provide(ports.FilterRulesName, r)
}
func (*Rules) Start(context.Context) error { return nil }
func (*Rules) Stop(context.Context) error  { return nil }
func (r *Rules) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := r.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (r *Rules) PrepareConfig(_ context.Context, view config.View) (func(), error) {
	var include, exclude []string
	var minMB, maxMB int64
	for _, field := range []struct {
		name   string
		target any
	}{{includeField, &include}, {excludeField, &exclude}, {minMBField, &minMB}, {maxMBField, &maxMB}} {
		if err := view.Get(field.name, field.target); err != nil {
			return nil, err
		}
	}
	if maxMB > 0 && minMB > maxMB {
		return nil, fmt.Errorf("minimum file size exceeds maximum")
	}
	next := &settings{include: extensions(include), exclude: extensions(exclude), min: mbToBytes(minMB), max: mbToBytes(maxMB)}
	return func() { r.settings.Store(next) }, nil
}

func (r *Rules) ShouldHandle(_ context.Context, input ports.FilterInput) (bool, ports.Reason) {
	s := r.settings.Load()
	if s == nil {
		return false, ports.SizeExcluded
	}
	ext := normalizeExtension(filepath.Ext(input.Name))
	if len(s.include) > 0 {
		if _, ok := s.include[ext]; !ok {
			return false, ports.ExtensionExcluded
		}
	}
	if _, ok := s.exclude[ext]; ok {
		return false, ports.ExtensionExcluded
	}
	if s.min > 0 && input.Size < s.min || s.max > 0 && input.Size > s.max {
		return false, ports.SizeExcluded
	}
	return true, ports.Allowed
}

func extensions(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[normalizeExtension(value)] = struct{}{}
	}
	return result
}

func normalizeExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value[0] == '.' {
		return value
	}
	return "." + value
}

func mbToBytes(mb int64) int64 {
	if mb <= 0 {
		return 0
	}
	const maxInt64 = int64(1<<63 - 1)
	const unit = 1024 * 1024
	if mb > maxInt64/unit {
		return maxInt64
	}
	return mb * unit
}
