package watch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/application"
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	configurationmanager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/componentconfig"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/pkg/config"
	pkgtclient "github.com/snakexgc/tdl/pkg/tclient"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

const liveHTTPHotStage = "http-hot"

// Explicit opt-in only. The normal suite never uses a real account. Original
// storage stays read-only; only the three session keys enter an in-memory store.
// No logout, session replacement, deletion, invitations or concurrency tests.
func TestLiveAccountSerialSmoke(t *testing.T) {
	if os.Getenv("TDL_LIVE_ACCOUNT_TEST") != "1" {
		t.Skip("requires explicit account owner authorization and TDL_LIVE_ACCOUNT_TEST=1")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	raw, err := os.ReadFile(filepath.Join(root, configurationmanager.Filename))
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	configuration := configurationmanager.New(rteconfig.File{Path: filepath.Join(root, configurationmanager.Filename)}, catalog)
	system, err := configuration.System(context.Background())
	require.NoError(t, err)
	cfg.Namespace, cfg.Debug = system.Namespace, system.Debug
	cfg, _, err = componentconfig.Load(context.Background(), configuration.Store(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		after, err := os.ReadFile(filepath.Join(root, configurationmanager.Filename))
		require.NoError(t, err)
		require.Equal(t, sha256.Sum256(raw), sha256.Sum256(after), "original configuration must stay unchanged")
	})
	accountFile := filepath.Join(root, ".tdl", cfg.Namespace)
	original, err := os.ReadFile(accountFile)
	require.NoError(t, err)
	originalHash := sha256.Sum256(original)
	t.Cleanup(func() {
		after, err := os.ReadFile(accountFile)
		require.NoError(t, err)
		require.Equal(t, originalHash, sha256.Sum256(after), "original account file must stay byte-for-byte unchanged")
	})
	db, err := bbolt.Open(accountFile, 0o600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	require.NoError(t, err)
	defer db.Close() // also prevents a second local writer while testing
	store := newMemoryTaskStorage()
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(cfg.Namespace))
		if bucket == nil {
			return fmt.Errorf("account bucket missing")
		}
		for _, key := range []string{tgauth.SessionKey, tgauth.AppKey, tgauth.FingerprintKey} {
			if value := bucket.Get([]byte(key)); len(value) > 0 {
				require.NoError(t, store.Set(context.Background(), key, value))
			}
		}
		return nil
	}))
	session, err := store.Get(context.Background(), tgauth.SessionKey)
	require.NoError(t, err)
	require.NotEmpty(t, session, "never attempt a new login during this test")
	cfg.Limit, cfg.PoolSize = 1, 1
	cfg.NTP, cfg.Debug = "", false
	cfg.Modules = config.ModulesConfig{}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	ctx = config.WithSource(logctx.With(ctx, zap.NewNop()), config.NewSource(cfg))
	registry := rte.NewRegistry()
	require.NoError(t, accounttelegram.Register(registry))
	host, err := registry.BuildStored(ctx, types.AccountID(cfg.Namespace), configuration.Store())
	require.NoError(t, err)
	host.Start(ctx)
	defer func() { require.NoError(t, host.Stop(context.Background())) }()
	credentials, err := host.Resolve(ports.TelegramCredentialsName)
	require.NoError(t, err)
	gate := &liveSerialGate{cancel: cancel}
	client, err := pkgtclient.New(ctx, pkgtclient.Options{
		Credentials: credentials.(ports.TelegramCredentials), KV: store, Account: types.AccountID(cfg.Namespace), Proxy: os.Getenv("TDL_LIVE_PROXY"), ReconnectTimeout: time.Second,
	}, false, gate)
	require.NoError(t, err)
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			return fmt.Errorf("existing session is not authorized; test will not log in")
		}
		t.Log("existing account authorized; original storage read-only; all test RPCs serialized")
		if stage := os.Getenv("TDL_LIVE_STAGE"); stage == "transfer" || stage == "forward" || stage == liveHTTPHotStage {
			return liveTransfers(t, ctx, root, cfg, store, client)
		}
		return nil
	})
	require.NoError(t, err)
	t.Logf("completed %d serialized RPCs; no account identifiers or credentials logged", gate.calls)
}

type liveSerialGate struct {
	mu     sync.Mutex
	next   time.Time
	calls  int
	cancel context.CancelFunc
}

func (g *liveSerialGate) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		if delay := time.Until(g.next); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		g.calls++
		err := next.Invoke(ctx, input, output)
		g.next = time.Now().Add(time.Second)
		if _, flood := tgerr.AsFloodWait(err); flood {
			g.cancel() // do not retry or wait out a server safety signal
		}
		return err
	}
}
