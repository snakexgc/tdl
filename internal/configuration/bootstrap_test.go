package configuration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/internal/migration"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	tokenField        = "token"
	otherAccount      = "Other"
	allowedUsersField = "allowed_users"
)

func readFile(t *testing.T, home string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, manager.Filename))
	require.NoError(t, err)
	return data
}

func TestFirstStartupCreatesCompleteConfigurationAndLiveSWC(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	service, err := configuration.Open(ctx, home, "")
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(home, "config.json"))
	require.NoDirExists(t, filepath.Join(home, "components"))
	catalog, err := application.Catalog()
	require.NoError(t, err)
	var doc manager.Document
	require.NoError(t, json.Unmarshal(readFile(t, home), &doc))
	require.Len(t, doc.Components, len(catalog.Definitions())-1)
	for _, definition := range catalog.Definitions() {
		if definition.Manifest.ID == manager.ID {
			continue
		}
		component := doc.Components[definition.Manifest.ID]
		for _, field := range definition.Manifest.Config {
			if field.ReplacedBy == "" {
				require.Contains(t, component.Values, field.Name, definition.Manifest.ID)
			}
		}
	}
	require.False(t, doc.Components["trigger.forward"].Enabled)
	host, err := application.ConfigurationHost(ctx, types.DefaultAccount, service)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	port, err := host.Resolve(ports.ConfigurationManagerName)
	require.NoError(t, err)
	require.Same(t, service, port)
	directory := rte.NewDirectory(catalog, service.Store())
	require.NoError(t, directory.Bind("configuration", func() *rte.Runtime { return host }))
	for _, entry := range directory.Configurations(ctx) {
		if entry.ID == manager.ID {
			require.Equal(t, rte.Running, entry.State)
		}
	}
}

func TestImportsSelectedSessionSettingsAndSecretsOnceWithoutChangingSources(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	bootstrap := legacy.DefaultConfig()
	bootstrap.Namespace = otherAccount
	bootstrap.Debug = true
	bootstrap.Bot.Token = "legacy-token"
	require.NoError(t, legacy.Save(filepath.Join(home, "config.json"), bootstrap))
	old, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	for _, account := range []string{"default", otherAccount} {
		cfg := legacy.DefaultConfig()
		cfg.Namespace, cfg.Bot.Token = account, "component-token-"+account
		dir, err := migration.EnsureComponents(ctx, home, cfg)
		require.NoError(t, err)
		catalog, err := application.Catalog()
		require.NoError(t, err)
		view, err := catalog.View(ctx, "downloader.aria2", map[string]any{"monitor_stall_seconds": 321})
		require.NoError(t, err)
		require.NoError(t, config.NewStore(dir).Save(ctx, "downloader.aria2", false, view))
		if account != bootstrap.Namespace {
			// Inactive settings must neither be imported nor block startup.
			require.NoError(t, os.WriteFile(filepath.Join(dir, "swc-downloader.aria2.json"), []byte("invalid JSON"), 0o600))
		}
	}
	require.NoError(t, os.Mkdir(filepath.Join(home, "components", "invalid-directory-name!"), 0o700))
	service, err := configuration.Open(ctx, home, "")
	require.NoError(t, err)
	{
		store := service.Store()
		token, err := store.Load(ctx, "console.bot")
		require.NoError(t, err)
		var value string
		data, err := json.Marshal(token.Values[tokenField])
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &value))
		require.Equal(t, "component-token-"+bootstrap.Namespace, value)
		aria, err := store.Load(ctx, "downloader.aria2")
		require.NoError(t, err)
		require.False(t, aria.Enabled)
		require.JSONEq(t, `321`, string(aria.Values["monitor_stall_seconds"].(json.RawMessage)))
	}
	after, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	require.Equal(t, old, after)
	saved := readFile(t, home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("invalid legacy JSON"), 0o600))
	_, err = configuration.Open(ctx, home, "missing-and-ignored-after-migration")
	require.NoError(t, err)
	require.Equal(t, saved, readFile(t, home))
}

func TestConcurrentComponentEditsAreAtomicAndSurviveSessionSwitch(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	service, err := configuration.Open(ctx, home, "")
	require.NoError(t, err)
	initial, err := service.System(ctx)
	require.NoError(t, err)
	next := initial
	next.Namespace = otherAccount
	catalog, err := application.Catalog()
	require.NoError(t, err)
	store := service.Store()
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for id, values := range map[string]map[string]any{
		"console.bot":      {tokenField: "private-token", allowedUsersField: []string{"9007199254740993"}},
		"downloader.aria2": {"monitor_stall_seconds": 456},
	} {
		view, err := catalog.View(ctx, id, values)
		require.NoError(t, err)
		wg.Go(func() { errors <- store.Save(ctx, id, false, view) })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, service.SetSystem(ctx, initial, next))
	bot, err := store.Load(ctx, "console.bot")
	require.NoError(t, err)
	require.JSONEq(t, `["9007199254740993"]`, string(bot.Values[allowedUsersField].(json.RawMessage)))
	aria, err := store.Load(ctx, "downloader.aria2")
	require.NoError(t, err)
	require.JSONEq(t, `456`, string(aria.Values["monitor_stall_seconds"].(json.RawMessage)))
	reopened, err := configuration.Open(ctx, home, "")
	require.NoError(t, err)
	afterSwitch, err := reopened.Store().Load(ctx, "console.bot")
	require.NoError(t, err)
	require.Equal(t, bot, afterSwitch)
	directory := rte.NewDirectory(catalog, store)
	public, err := json.Marshal(directory.Configurations(ctx))
	require.NoError(t, err)
	require.NotContains(t, string(public), "private-token")
	revision, err := store.Revision(ctx, "console.bot")
	require.NoError(t, err)
	require.NoError(t, directory.PatchWithRevision(ctx, "console.bot", map[string]any{tokenField: "", allowedUsersField: []string{"42"}}, revision))
	require.ErrorIs(t, directory.PatchWithRevision(ctx, "console.bot", map[string]any{allowedUsersField: []string{}}, revision), rte.ErrConfigurationConflict)
	bot, err = store.Load(ctx, "console.bot")
	require.NoError(t, err)
	require.JSONEq(t, `"private-token"`, string(bot.Values[tokenField].(json.RawMessage)))
	before := readFile(t, home)
	require.Error(t, directory.Patch(ctx, "downloader.aria2", map[string]any{"connect_retry_ms": 60000, "connect_retry_max_ms": 100}))
	require.Error(t, directory.SetEnabled(ctx, manager.ID, false))
	require.ErrorIs(t, service.SetSystem(ctx, initial, next), ports.ErrConfigurationConflict)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, directory.Patch(canceled, "console.bot", map[string]any{tokenField: "changed"}), context.Canceled)
	require.Equal(t, before, readFile(t, home))
}

func TestMalformedUnifiedConfigurationNeverFallsBackToLegacy(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"system":{"namespace":"default","debug":null}}`,
		`{"version":1,"system":{"namespace":"default","debug":true,"debug":false}}`,
		`{"version":1,"system":{"namespace":"default"},"components":{"console.bot":null}}`,
		`null`, `{}`, `{"version":2,"system":{"namespace":"default"}}`,
		`{"version":1,"system":{"namespace":"default","typo":1}}`,
		`{"version":1,"system":{"namespace":"default"},"components":{"unknown":{"enabled":true,"values":{}}}}`,
		`{"version":1,"system":{"namespace":"default"},"components":{"console.bot":{"enabled":false,"values":{"tokne":"secret"}}}}`,
		`{"version":1,"system":{"namespace":"default"},"accounts":{}}`,
		`{"version":1,"system":{"namespace":"default"}} {}`,
	} {
		t.Run(data, func(t *testing.T) {
			home := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(home, manager.Filename), []byte(data), 0o600))
			_, err := configuration.Open(context.Background(), home, "")
			require.Error(t, err)
			require.Equal(t, []byte(data), readFile(t, home))
			require.NoFileExists(t, filepath.Join(home, "config.json"))
		})
	}
}

func TestSystemSettingsAndAccountSelectionUseUnifiedFile(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	service, err := configuration.Open(ctx, home, "")
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "panel.webui", map[string]any{"port": 33445, "password": "keep-password"})
	require.NoError(t, err)
	require.NoError(t, service.Store().Save(ctx, "panel.webui", true, view))
	_, err = configuration.Install(ctx, service)
	require.NoError(t, err)
	before := legacy.Get()
	next, err := legacy.Clone(before)
	require.NoError(t, err)
	next.Debug = true
	require.NoError(t, legacy.CompareAndSet(ctx, before, next))
	changed, err := legacy.SelectNamespace(ctx, "default", "Second")
	require.NoError(t, err)
	require.True(t, changed)
	system, err := service.System(ctx)
	require.NoError(t, err)
	require.Equal(t, ports.SystemConfiguration{Namespace: "Second", Debug: true}, system)
	_, err = configuration.Install(ctx, service)
	require.NoError(t, err)
	panel, err := service.Store().Load(ctx, "panel.webui")
	require.NoError(t, err)
	require.JSONEq(t, `33445`, string(panel.Values["port"].(json.RawMessage)))
	require.JSONEq(t, `"keep-password"`, string(panel.Values["password"].(json.RawMessage)))
	require.NoFileExists(t, filepath.Join(home, "config.json"))
}
