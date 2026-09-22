package runtime

import (
	"fmt"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/rte"
)

func (m *Manager) ResolveComponentPort(id, name string) (any, error) {
	if m.directory == nil {
		return nil, fmt.Errorf("component directory is unavailable")
	}
	return m.directory.ResolveComponentPort(id, name)
}

// The composition boundary binds resource lifetimes once. Query and save paths
// discover the owning component instead of maintaining component-ID switches.
func (m *Manager) initDirectory() error {
	catalog, err := application.Catalog()
	if err != nil {
		return err
	}
	directory := rte.NewDirectory(catalog, m.savedStore).WithActiveStore(m.componentStore)
	for _, binding := range []struct {
		name    string
		resolve func() *rte.Runtime
	}{
		{"configuration", func() *rte.Runtime { return m.configurationHost }},
		{"time", func() *rte.Runtime { return m.timeHost }},
		{"account", func() *rte.Runtime { return m.accountHost }},
		{"policies", func() *rte.Runtime { return m.policies }},
		{"bot", func() *rte.Runtime { return m.botComponents }},
		{localExecutorID, func() *rte.Runtime { return m.localHost }},
		{"panel", func() *rte.Runtime { return m.panelHost }},
		{"intents", func() *rte.Runtime { return m.intentHost }},
		{moduleIDAria2, func() *rte.Runtime {
			if m.aria2Mgr == nil {
				return nil
			}
			return m.aria2Mgr.Host()
		}},
		{"range", func() *rte.Runtime {
			if m.httpService == nil {
				return nil
			}
			return m.httpService.Proxy().Host()
		}},
		{"forward", func() *rte.Runtime {
			if m.forwardQueue == nil {
				return nil
			}
			return m.forwardQueue.Host()
		}},
		{"downloads", func() *rte.Runtime { return m.downloadHost }},
	} {
		resolve := binding.resolve
		if err := directory.Bind(binding.name, func() *rte.Runtime {
			m.mu.Lock()
			defer m.mu.Unlock()
			return resolve()
		}); err != nil {
			return err
		}
	}
	for _, process := range []*rte.Process{m.botProcess, m.aria2Process, m.panelProcess} {
		if process == nil {
			continue
		}
		health := process.Health()
		if err := directory.Observe(health.Components[0].ID, process.Health); err != nil {
			return err
		}
	}
	observers := map[string]func() rte.Health{}
	if m.httpCtrl != nil {
		observers["http"] = m.httpCtrl.Health
	}
	if m.watchCtrl != nil {
		observers["watch"] = m.watchCtrl.Health
	}
	for name, observe := range observers {
		if err := directory.Observe(name, observe); err != nil {
			return err
		}
	}
	if m.reconciler != nil {
		if err := directory.Observe("assembly", m.reconciler.Health); err != nil {
			return err
		}
	}
	m.directory = directory
	return nil
}
