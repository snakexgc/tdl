package reaction

import (
	"context"
	"strings"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID            = "trigger.reaction"
	reactionTitle = "表情触发"
)

const (
	downloadField = "download"
	forwardField  = "forward"
)

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "triggers", Title: reactionTitle, Order: 60, SettingsURL: "/config?tab=download"},
		ID:      ID, Title: reactionTitle,
		Provides: []manifest.Port{manifest.PortOf[ports.ReactionTrigger](ports.ReactionTriggerName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: downloadField, Title: "下载表情", Help: "每行一个表情；留空允许任意表情触发下载。", Type: manifest.Strings, Default: []string{}},
			{Name: forwardField, SettingsTab: "forward", SettingsSection: reactionTitle, Title: "转发表情", Help: "每行一个表情；留空关闭表情触发转发，监听转发仍按规则工作。", Type: manifest.Strings, Default: []string{}},
		},
	}, "download", reactionTitle), func() rte.Component { return &Trigger{} })
}

type Trigger struct {
	mu                sync.Mutex
	account           types.AccountID
	download, forward map[string]bool
	claims            map[ports.ReactionKey]bool
	stopped           bool
}

func (t *Trigger) Init(ctx context.Context, k rte.Kernel) error {
	t.account = k.Account
	if err := t.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.ReactionTriggerName, t)
}
func (*Trigger) Start(context.Context) error { return nil }
func (t *Trigger) Stop(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	clear(t.claims)
	return nil
}

func (t *Trigger) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := t.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (t *Trigger) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var download, forward []string
	if err := view.Get(downloadField, &download); err != nil {
		return nil, err
	}
	if err := view.Get(forwardField, &forward); err != nil {
		return nil, err
	}
	d, f := reactionSet(download), reactionSet(forward)
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.download, t.forward = d, f
		// Configuration changes must not release claims for accepted work.
		if t.claims == nil {
			t.claims = make(map[ports.ReactionKey]bool)
		}
	}, nil
}

func (t *Trigger) Matches(ctx context.Context, in ports.ReactionInput) bool {
	if ctx.Err() != nil || in.Partial {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || in.Account != t.account {
		return false
	}
	wanted := t.download
	if in.Forward {
		wanted = t.forward
	}
	for _, r := range in.Reactions {
		if r.Mine && (len(wanted) == 0 || wanted[strings.TrimSpace(r.Value)]) {
			return true
		}
	}
	return false
}

func (t *Trigger) Claim(ctx context.Context, key ports.ReactionKey) bool {
	if ctx.Err() != nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || key.Account != t.account || t.claims[key] {
		return false
	}
	t.claims[key] = true
	return true
}

func (t *Trigger) Forget(key ports.ReactionKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key.Account == t.account {
		delete(t.claims, key)
	}
}

func reactionSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = true
		}
	}
	return result
}
