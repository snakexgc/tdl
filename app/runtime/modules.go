package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/snakexgc/tdl/app/webui"
	"github.com/snakexgc/tdl/internal/componentconfig"
	"github.com/snakexgc/tdl/pkg/config"
)

type moduleBinding struct {
	id, component string
	set           func(*config.Config, bool)
	state         func(*config.Config) webui.ModuleState
}

func (m *Manager) modules() []moduleBinding {
	return []moduleBinding{
		{"webui", "panel.webui", func(c *config.Config, v bool) { c.Modules.WebUI = v }, m.panelState},
		{moduleIDBot, "console.bot", func(c *config.Config, v bool) { c.Modules.Bot = v }, m.botState},
		{moduleIDWatch, "trigger.download", func(c *config.Config, v bool) { c.Modules.Watch = v }, m.watchState},
		{moduleIDHTTP, "proxy.range", func(c *config.Config, v bool) { c.Modules.HTTP = v }, m.httpState},
		{moduleIDAria2, "downloader.aria2", func(c *config.Config, v bool) { c.Modules.Aria2 = v }, m.aria2State},
		{moduleIDForward, "trigger.forward", func(c *config.Config, v bool) { c.Modules.Forward = v }, m.forwardState},
	}
}

func (m *Manager) panelState(cfg *config.Config) webui.ModuleState {
	running := m.panelProcess != nil && m.panelProcess.Running()
	status := "stopped"
	if running {
		status = moduleStatusRunning
	}
	return webui.ModuleState{ID: "webui", Name: "Web 管理面板", Description: "关闭后可通过组件配置文件重新启用。", Enabled: cfg != nil && cfg.Modules.WebUI, Running: running, CanToggle: true, Status: status}
}

func (m *Manager) ModuleStates() []webui.ModuleState {
	cfg := config.From(m.parent)
	states := []webui.ModuleState{}
	for _, binding := range m.modules() {
		states = append(states, binding.state(cfg))
	}
	return states
}

func (m *Manager) SetModuleEnabled(ctx context.Context, id string, enabled bool) (webui.ModuleState, error) {
	if m == nil {
		return webui.ModuleState{}, fmt.Errorf("module manager is not initialized")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	id = strings.ToLower(strings.TrimSpace(id))
	for _, binding := range m.modules() {
		if binding.id != id {
			continue
		}
		if m.componentStore != nil {
			if err := m.directory.SetEnabled(ctx, binding.component, enabled); err != nil {
				return webui.ModuleState{}, err
			}
		} else {
			next, err := config.Clone(config.Get())
			if err != nil {
				return webui.ModuleState{}, err
			}
			binding.set(next, enabled)
			if err := config.Set(next); err != nil {
				return webui.ModuleState{}, err
			}
		}
		cfg, flags, err := componentconfig.Load(ctx, m.componentStore, config.Get())
		if err != nil {
			return webui.ModuleState{}, err
		}
		if m.configSource != nil {
			m.configSource.Replace(cfg)
		}
		m.mu.Lock()
		m.configured = flags
		m.mu.Unlock()
		if err := m.applyConfigLocked(cfg, m.applyVersion.Add(1), true); err != nil {
			return webui.ModuleState{}, err
		}
		return binding.state(cfg), nil
	}
	return webui.ModuleState{}, fmt.Errorf("unknown module %q", id)
}
