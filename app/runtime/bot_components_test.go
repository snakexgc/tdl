package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	legacyconfig "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

func TestBotFeatureTogglesPreserveTransportAndConsole(t *testing.T) {
	ctx := context.Background()
	cfg := legacyconfig.DefaultConfig()
	cfg.Modules = legacyconfig.ModulesConfig{Bot: true}
	cfg.Bot.Token = "test-token"
	cfg.Bot.AllowedUsers = []int64{7}
	store := newStoppedComponentStore(t)
	saveComponent(t, store, consoleComponentID, true, map[string]any{"token": cfg.Bot.Token, "allowed_users": []string{"7"}})
	m := NewManager(legacyconfig.WithSource(ctx, legacyconfig.NewSource(cfg)), nil, nil, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	transport := &notificationRecorder{}
	host, console, _, err := application.BotHost(m.parent, m.downloadAccount, transport, cfg.Bot.AllowedUsers, m.componentStore)
	require.NoError(t, err)
	m.botComponents = host
	m.botRefresh = func(ctx context.Context) error {
		return application.ReconcileBotHost(ctx, host, m.downloadAccount, transport, cfg.Bot.AllowedUsers, m.componentStore)
	}
	started := make(chan struct{})
	exited := make(chan struct{})
	_, err = m.botProcess.Start(func(ctx context.Context) error {
		close(started)
		defer close(exited)
		<-ctx.Done()
		return host.Stop(context.Background())
	}, rte.Recovery{})
	require.NoError(t, err)
	<-started
	m.ApplyConfig(cfg)
	m.transitionWG.Wait()
	accountOwner := m.accountHost
	require.True(t, console.PrivateCommand("update_tdl"))
	for _, id := range []string{"notify.telegram", "update.self"} {
		require.NoError(t, m.SetComponentEnabled(ctx, id, false, ""))
		m.transitionWG.Wait()
		select {
		case <-exited:
			t.Fatal("feature toggle stopped the Bot transport")
		default:
		}
		require.Same(t, accountOwner, m.accountHost)
		current, err := host.Resolve(ports.ConsoleName)
		require.NoError(t, err)
		require.Same(t, console, current)
		require.True(t, console.Allowed(m.downloadAccount, 7))
		if id == "notify.telegram" {
			_, err = host.Resolve(ports.NotificationsName)
			require.Error(t, err)
		} else {
			require.False(t, console.PrivateCommand("update_tdl"))
		}
		require.NoError(t, m.SetComponentEnabled(ctx, id, true, ""))
		m.transitionWG.Wait()
	}
	require.True(t, console.PrivateCommand("update_tdl"))
	_, err = host.Resolve(ports.NotificationsName)
	require.NoError(t, err)
}

type notificationRecorder struct{ recipients []int64 }

func (r *notificationRecorder) Send(_ context.Context, id int64, _ string) (int, error) {
	r.recipients = append(r.recipients, id)
	return 1, nil
}
func (*notificationRecorder) Edit(context.Context, int64, int, string) error { return nil }

func TestBotComponentConfigurationPersistence(t *testing.T) {
	ctx := context.Background()
	store := newComponentStore(t)
	transport := &notificationRecorder{}
	host, console, notifications, err := application.BotHost(ctx, "", transport, []int64{42}, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{componentStore: store, botComponents: host}
	require.NoError(t, manager.initDirectory())
	configurations, editable := manager.ComponentConfigurations()
	require.True(t, editable)
	require.Len(t, configurations, 19)
	// Missing documents use schema defaults, never the legacy permission list.
	require.False(t, console.Allowed(types.DefaultAccount, 42))
	const consoleID = consoleComponentID
	const notifyID = "notify.telegram"
	require.NoError(t, manager.SaveComponentConfiguration(ctx, consoleID, map[string]any{testAllowedUsersField: []string{"7"}}))
	require.True(t, console.Allowed(types.DefaultAccount, 7))
	require.NoError(t, manager.SaveComponentConfiguration(ctx, notifyID, map[string]any{"recipients": []string{"-100"}}))
	_, err = notifications.Send(ctx, "hello")
	require.NoError(t, err)
	require.Equal(t, []int64{-100}, transport.recipients)
	require.Error(t, manager.SaveComponentConfiguration(ctx, consoleID, map[string]any{testAllowedUsersField: []string{"invalid"}}))
	require.True(t, console.Allowed(types.DefaultAccount, 7))
	require.NoError(t, host.Stop(ctx))
	restarted, restored, _, err := application.BotHost(ctx, types.DefaultAccount, transport, []int64{42}, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, restarted.Stop(ctx)) }()
	require.True(t, restored.Allowed(types.DefaultAccount, 7))
	require.False(t, restored.Allowed(types.DefaultAccount, 42))
}

func TestDisabledNotificationsDoNotDisableConsole(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	store := newComponentStore(t)
	view, err := catalog.View(ctx, "notify.telegram", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "notify.telegram", false, view))
	view, err = catalog.View(ctx, consoleComponentID, map[string]any{testAllowedUsersField: []string{"7"}})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, consoleComponentID, true, view))
	host, console, notifications, err := application.BotHost(ctx, types.DefaultAccount, &notificationRecorder{}, nil, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	require.True(t, console.Allowed(types.DefaultAccount, 7))
	require.Nil(t, notifications)
}

const testAllowedUsersField = "allowed_users"
