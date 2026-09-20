package webui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/componentconfig"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

// Opt-in browser fixture. Uses disposable storage and simulated backends only.
// TDL_WEBUI_PREVIEW=1 go test ./app/webui -run TestBrowserPreview -v -timeout 1h
func TestBrowserPreview(t *testing.T) {
	if os.Getenv("TDL_WEBUI_PREVIEW") != "1" {
		t.Skip("interactive browser fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logs := logging.New(nil)
	ctx = logging.WithStore(ctx, logs)
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	cfg.Modules.WebUI = true
	cfg.Downloader.Mode = "local"
	catalog, err := application.Catalog()
	require.NoError(t, err)
	for _, definition := range catalog.Definitions() {
		require.NoError(t, logs.Write(logging.Entry{Component: definition.Manifest.ID, Account: cfg.Namespace, Level: logInfoLevel, Kind: logRuntimeKind, Message: definition.Manifest.Title + "已启动"}))
	}
	for count := range 220 {
		require.NoError(t, logs.Write(logging.Entry{Component: logLocalComponent, Account: cfg.Namespace, Level: logInfoLevel, Kind: logRuntimeKind, Message: fmt.Sprintf("本地下载已完成 · 示例任务 %d", count)}))
	}
	require.NoError(t, logs.Write(logging.Entry{Component: logForwardComponent, Account: cfg.Namespace, Level: fieldError, Kind: logDiagnosticKind, Message: "转发任务需要重试", Details: `{"operation":"forward.queue","error":"fixture timeout"}`}))
	store := rteconfig.NewStore(t.TempDir())
	docs, err := componentconfig.Export(cfg, catalog)
	require.NoError(t, err)
	for id, doc := range docs {
		view, err := catalog.View(ctx, id, doc.Values)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, id, doc.Enabled, view))
	}
	source := config.NewSource(cfg)
	manager := &previewComponents{Directory: rte.NewDirectory(catalog, store), store: store, source: source, cfg: cfg}
	engine, err := kv.New(kv.DriverFile, map[string]any{"path": filepath.Join(t.TempDir(), "state")})
	require.NoError(t, err)
	defer engine.Close()
	kvd, err := engine.Open(cfg.Namespace)
	require.NoError(t, err)
	s := NewServer(Options{Context: config.WithSource(ctx, source), Namespace: cfg.Namespace, NamespaceKV: kvd, KVEngine: engine, ComponentManager: manager, ComponentStore: store, Dialogs: previewDialogs{}})
	listener, err := net.Listen("tcp", "127.0.0.1:22359")
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/", s.routes())
	mux.HandleFunc("POST /__preview/stop", func(w http.ResponseWriter, r *http.Request) { cancel(); w.WriteHeader(http.StatusNoContent) })
	server := httptest.NewUnstartedServer(mux)
	server.Listener = listener
	server.Start()
	defer server.Close()
	fmt.Println("PREVIEW_URL=" + server.URL + "/login")
	<-ctx.Done()
}

type previewComponents struct {
	*rte.Directory
	store   *rteconfig.Store
	source  *config.Source
	cfg     *config.Config
	version atomic.Uint64
}

func (p *previewComponents) ComponentConfigurations() ([]rte.Configuration, bool) {
	return p.Configurations(context.Background()), true
}

func (p *previewComponents) SaveComponentConfiguration(ctx context.Context, id string, values map[string]any) error {
	return p.SaveComponentConfigurationVersion(ctx, id, values, "")
}

func (p *previewComponents) SaveComponentConfigurationVersion(ctx context.Context, id string, values map[string]any, revision string) error {
	if err := p.PatchWithRevision(ctx, id, values, revision); err != nil {
		return err
	}
	return p.refresh(ctx)
}

func (p *previewComponents) SetComponentEnabled(ctx context.Context, id string, enabled bool, revision string) error {
	if err := p.SetEnabledWithRevision(ctx, id, enabled, revision); err != nil {
		return err
	}
	return p.refresh(ctx)
}

func (p *previewComponents) refresh(ctx context.Context) error {
	cfg, _, err := componentconfig.Load(ctx, p.store, p.cfg)
	if err != nil {
		return err
	}
	p.source.Replace(cfg)
	p.version.Add(1)
	return nil
}
func (p *previewComponents) ConfigurationVersion() uint64 { return p.version.Load() }

func (p *previewComponents) ComponentHealth() []rte.Health {
	return []rte.Health{{Account: types.AccountID(p.cfg.Namespace), Components: []rte.ComponentHealth{{Status: rte.Status{ID: logForwardComponent, State: rte.Running}}, {Status: rte.Status{ID: logLocalComponent, State: rte.Running}}}, Events: []types.DiagnosticEvent{{Sequence: 1, Component: logForwardComponent, Operation: "forward.queue", Message: "fixture timeout", At: time.Now()}}}}
}

type previewDialogs struct{}

func (previewDialogs) Dialogs(context.Context) ([]types.Dialog, error) {
	return []types.Dialog{{Ref: "channel:11", Title: "产品交流", Username: "product", Kind: "group"}, {Ref: "channel:12", Title: "开发团队", Kind: "group"}, {Ref: "user:21", Title: "通知机器人", Username: "notice_bot", Kind: logBotAdapter}, {Ref: "channel:13", Title: "项目归档", Kind: "channel"}}, nil
}
