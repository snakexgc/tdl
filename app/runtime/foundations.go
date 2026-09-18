package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

const (
	policyResource      = "host.policies"
	accountResource     = "host.account"
	downloadResource    = "host.downloads"
	watchPolicyResource = "host.watch.policies"
)

// The long-lived owners use the same stop graph as their transport consumers.
// A failed drain retains both the host pointer and every required provider.
func (m *Manager) foundationUnits(cfg *config.Config) []rte.ManagedUnit {
	policyStates := map[string]bool{}
	catalog, err := application.Catalog()
	if err != nil {
		panic(err)
	}
	for _, definition := range catalog.Definitions() {
		if definition.Factory != nil && definition.Host == "" {
			policyStates[definition.Manifest.ID] = m.componentEnabled(definition.Manifest.ID)
		}
	}
	return []rte.ManagedUnit{
		{
			ID: policyResource, Enabled: true, Revision: revision(policyStates),
			Running: func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.policies != nil },
			Start: func(context.Context) error {
				host, filter, naming, err := newPolicyHostStored(m.parent, cfg, m.componentStore)
				m.mu.Lock()
				m.policies, m.filter, m.naming, m.policyErr = host, filter, naming, err
				m.mu.Unlock()
				if host == nil {
					return err
				}
				return nil // missing watcher policies must not stop independent ports
			},
			Update: func(ctx context.Context) error {
				m.mu.Lock()
				host := m.policies
				m.mu.Unlock()
				if host != nil && m.componentStore == nil {
					return host.ReconfigureBatch(ctx, daemonComponentValues(cfg))
				}
				return nil
			},
			Stop: stopResource(func(ctx context.Context) error {
				m.mu.Lock()
				host := m.policies
				m.mu.Unlock()
				if host == nil {
					return nil
				}
				if err := host.Stop(ctx); err != nil {
					return err
				}
				m.mu.Lock()
				m.policies, m.filter, m.naming = nil, nil, nil
				m.mu.Unlock()
				return nil
			}),
		},
		{
			ID: accountResource, Enabled: true, Requires: []string{policyResource},
			Revision: revision(cfg.Telegram, config.EffectiveProxy(cfg), cfg.NTP, cfg.Delay, cfg.ReconnectTimeout),
			Running:  func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.accountHost != nil },
			Start: func(context.Context) error {
				connections := tgauth.NewConnections(m.parent)
				m.mu.Lock()
				m.connections = connections
				m.mu.Unlock()
				host, err := application.AccountResourceHost(m.parent, m.downloadAccount, connections, login.SessionProbe{Options: m.sessionOptions})
				if err != nil {
					return err
				}
				m.mu.Lock()
				m.accountHost = host
				m.mu.Unlock()
				value, err := host.Resolve(ports.AccountSessionName)
				if err != nil {
					return err
				}
				m.mu.Lock()
				m.sessionPort = value.(ports.AccountSession)
				m.mu.Unlock()
				return nil
			},
			Stop: stopResource(func(ctx context.Context) error {
				m.mu.Lock()
				host, connections := m.accountHost, m.connections
				m.mu.Unlock()
				var err error
				if host != nil {
					err = host.Stop(ctx)
				} else if connections != nil {
					err = connections.Stop(ctx)
				}
				if err == nil {
					m.mu.Lock()
					m.accountHost, m.sessionPort = nil, nil
					m.mu.Unlock()
				}
				return err
			}),
		},
		{
			ID: downloadResource, Enabled: m.componentEnabled("download.control"), Revision: revision(cfg.Downloader.Mode),
			Running: func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.downloadHost != nil },
			Start:   func(context.Context) error { return m.initDownloadControl(m.parent) },
			Stop: stopResource(func(ctx context.Context) error {
				m.mu.Lock()
				host := m.downloadHost
				m.mu.Unlock()
				if host == nil {
					return nil
				}
				if err := host.Stop(ctx); err != nil {
					return err
				}
				m.mu.Lock()
				m.downloadHost, m.downloadPort = nil, nil
				m.mu.Unlock()
				return nil
			}),
		},
		{
			ID: watchPolicyResource, Enabled: true, Requires: []string{policyResource},
			Start: func(context.Context) error {
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.policyErr != nil {
					return fmt.Errorf("watch policies unavailable: %w", m.policyErr)
				}
				return nil
			}, Stop: func(context.Context) error { return nil },
		},
	}
}
