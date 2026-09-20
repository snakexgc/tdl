// Package forwardrules owns many-to-many routing, independently of Telegram
// transport, the queue and the panel. A saved policy is an immutable snapshot.
package forwardrules

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "forward.rules"

const defaultMode = "default"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "forward", Title: "转发管理", Order: 20, SettingsURL: "/config?tab=forward"},
		ID:      ID, Title: "分组转发规则",
		Provides: []manifest.Port{manifest.PortOf[ports.ForwardRules](ports.ForwardRulesName, 1, 0)},
		Config:   []manifest.ConfigField{{Name: "rules", Title: "来源 → 目标", Type: manifest.Objects, Default: []types.ForwardRule{}, Editor: "/static/js/forward-rules.js"}},
	}, "forward", "转发规则"), func() rte.Component { return &Rules{} })
}

type (
	routes map[types.ChatRef][]types.ForwardDestination
	Rules  struct{ current atomic.Pointer[routes] }
)

func (r *Rules) Init(ctx context.Context, k rte.Kernel) error {
	if err := r.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.ForwardRulesName, r)
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

func (r *Rules) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rules []types.ForwardRule
	if err := view.Get("rules", &rules); err != nil {
		return nil, err
	}
	if len(rules) > 200 {
		return nil, fmt.Errorf("最多支持 200 条转发规则")
	}
	next := routes{}
	ids := map[string]bool{}
	for _, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" || ids[rule.ID] {
			return nil, fmt.Errorf("规则 ID 为空或重复：%s", rule.ID)
		}
		ids[rule.ID] = true
		if utf8.RuneCountInString(rule.Name) > 100 || len(rule.Sources) > 500 || len(rule.Targets) > 100 {
			return nil, fmt.Errorf("规则 %s 超过名称或来源/目标数量限制", rule.ID)
		}
		if len(rule.Sources) == 0 || len(rule.Targets) == 0 {
			return nil, fmt.Errorf("规则 %s 必须选择来源和目标", rule.ID)
		}
		if rule.Mode != defaultMode && rule.Mode != "clone" {
			return nil, fmt.Errorf("规则 %s 的转发模式无效", rule.ID)
		}
		for _, source := range rule.Sources {
			if !validRef(source, false) {
				return nil, fmt.Errorf("无效来源：%s", source)
			}
			for _, target := range rule.Targets {
				if !validRef(target, true) || source == target {
					return nil, fmt.Errorf("无效目标或转发给自身：%s", target)
				}
				if !rule.Enabled {
					continue
				}
				// First matching rule wins for each destination, not for the
				// whole message. Overlapping rules still fan out to other chats.
				duplicate := false
				for _, route := range next[source] {
					duplicate = duplicate || route.Target == target
				}
				if !duplicate {
					next[source] = append(next[source], types.ForwardDestination{RuleID: rule.ID, Target: target, Mode: rule.Mode, Silent: rule.Silent})
				}
			}
		}
	}
	visiting, visited := map[types.ChatRef]bool{}, map[types.ChatRef]bool{}
	var cycle func(types.ChatRef) bool
	cycle = func(source types.ChatRef) bool {
		if visiting[source] {
			return true
		}
		if visited[source] {
			return false
		}
		visiting[source] = true
		for _, target := range next[source] {
			if cycle(target.Target) {
				return true
			}
		}
		visiting[source], visited[source] = false, true
		return false
	}
	for source := range next {
		if cycle(source) {
			return nil, fmt.Errorf("转发规则形成循环，请移除环路后保存")
		}
	}
	return func() { r.current.Store(&next) }, nil
}

func validRef(ref types.ChatRef, self bool) bool {
	if self && ref == "self" {
		return true
	}
	parts := strings.Split(string(ref), ":")
	if len(parts) != 2 || (parts[0] != "user" && parts[0] != "chat" && parts[0] != "channel") {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == parts[1]
}

func (r *Rules) Destinations(_ context.Context, source types.ChatRef) []types.ForwardDestination {
	current := r.current.Load()
	if current == nil {
		return nil
	}
	return append([]types.ForwardDestination(nil), (*current)[source]...)
}
