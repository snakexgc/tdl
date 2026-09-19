package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

type notificationRecorder struct{ recipients []int64 }

func (r *notificationRecorder) Send(_ context.Context, id int64, _ string) (int, error) {
	r.recipients = append(r.recipients, id)
	return 1, nil
}
func (*notificationRecorder) Edit(context.Context, int64, int, string) error { return nil }

func TestBotComponentConfigurationPersistence(t *testing.T) {
	ctx := context.Background()
	store := config.NewStore(t.TempDir())
	transport := &notificationRecorder{}
	host, console, notifications, err := application.BotHost(ctx, "", transport, []int64{42}, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{componentStore: store, botComponents: host}
	require.NoError(t, manager.initDirectory())
	configurations, editable := manager.ComponentConfigurations()
	require.True(t, editable)
	require.Len(t, configurations, 18)
	// Missing documents use schema defaults, never the legacy permission list.
	require.False(t, console.Allowed(types.DefaultAccount, 42))
	const consoleID = "console.bot"
	const notifyID = "notify.telegram"
	require.NoError(t, manager.SaveComponentConfiguration(ctx, consoleID, map[string]any{testAllowedUsersField: []string{"7"}}))
	require.True(t, console.Allowed(types.DefaultAccount, 7))
	require.NoError(t, manager.SaveComponentConfiguration(ctx, notifyID, map[string]any{"recipients": []string{"-100"}}))
	_, err = notifications.Send(ctx, types.DefaultAccount, "hello")
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
	store := config.NewStore(t.TempDir())
	view, err := catalog.View(ctx, "notify.telegram", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "notify.telegram", false, view))
	view, err = catalog.View(ctx, "console.bot", map[string]any{testAllowedUsersField: []string{"7"}})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "console.bot", true, view))
	host, console, notifications, err := application.BotHost(ctx, types.DefaultAccount, &notificationRecorder{}, nil, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	require.True(t, console.Allowed(types.DefaultAccount, 7))
	require.Nil(t, notifications)
}

const testAllowedUsersField = "allowed_users"
