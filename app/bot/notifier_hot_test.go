package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestNotifierResolvesReenabledComponent(t *testing.T) {
	ctx := context.Background()
	store := configtest.NewStore()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "notify.telegram", map[string]any{"recipients": []string{"7"}})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "notify.telegram", true, view))
	sender := &fakeBotAPI{}
	transport := &botNotificationTransport{sender: sender}
	host, _, service, err := application.BotHost(ctx, types.DefaultAccount, transport, nil, store)
	require.NoError(t, err)
	notifier := &botNotifier{host: host, service: service, account: types.DefaultAccount}
	t.Cleanup(notifier.Close)
	require.Len(t, notifier.SendAndTrack(ctx, "before"), 1)
	require.NoError(t, store.Save(ctx, "notify.telegram", false, view))
	require.NoError(t, application.ReconcileBotHost(ctx, host, types.DefaultAccount, transport, nil, store))
	require.Empty(t, notifier.SendAndTrack(ctx, "disabled"))
	require.NoError(t, store.Save(ctx, "notify.telegram", true, view))
	require.NoError(t, application.ReconcileBotHost(ctx, host, types.DefaultAccount, transport, nil, store))
	require.Len(t, notifier.SendAndTrack(ctx, "after"), 1)
	require.Equal(t, []string{"before", "after"}, sender.messagesText())
}
