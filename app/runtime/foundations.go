package runtime

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

const (
	policyResource   = "host.policies"
	accountResource  = "host.account"
	downloadResource = "host.downloads"
)

// The long-lived owners use the same stop graph as their transport consumers.
// A failed drain retains both the host pointer and every required provider.
func (m *Manager) foundationUnits(cfg *config.Config) []rte.ManagedUnit {
	return []rte.ManagedUnit{
		{
			ID: policyResource, Enabled: true,
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
				if host == nil {
					return nil
				}
				catalog, err := application.Catalog()
				if err != nil {
					return err
				}
				desired, err := buildCatalogPolicyHost(ctx, cfg, m.componentStore, catalog)
				if err != nil {
					return err
				}
				if err := host.ReconcileComponents(m.parent, desired); err != nil {
					return err
				}
				filter, filterErr := host.Resolve(ports.FilterRulesName)
				naming, namingErr := host.Resolve(ports.NamingRulesName)
				m.mu.Lock()
				m.filter, _ = filter.(ports.FilterRules)
				m.naming, _ = naming.(ports.NamingRules)
				m.policyErr = errors.Join(filterErr, namingErr)
				m.mu.Unlock()
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
			// Credential policy comes from the immutable startup store. It
			// must not close an authenticated connection that is still in use.
			Revision: revision(config.EffectiveProxy(cfg), cfg.Delay, cfg.ReconnectTimeout),
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
			ID: downloadResource, Enabled: m.componentEnabled("download.control"),
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
	}
}
