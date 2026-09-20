package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

const (
	exampleID     = "example.feature"
	exampleAction = "example.action"
)

type exampleFeature struct{ label atomic.Value }

func (s *exampleFeature) Init(ctx context.Context, kernel rte.Kernel) error {
	if err := s.Reconfigure(ctx, kernel.Config); err != nil {
		return err
	}
	return kernel.Provide(exampleAction, s)
}
func (*exampleFeature) Start(context.Context) error { return nil }
func (*exampleFeature) Stop(context.Context) error  { return nil }
func (s *exampleFeature) PrepareConfig(_ context.Context, view rteconfig.View) (func(), error) {
	var label string
	if err := view.Get("label", &label); err != nil {
		return nil, err
	}
	return func() { s.label.Store(label) }, nil
}

func (s *exampleFeature) Reconfigure(ctx context.Context, view rteconfig.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err == nil {
		commit()
	}
	return err
}

func (s *exampleFeature) Handle(context.Context, types.WebRequest) (types.WebResponse, error) {
	return types.WebResponse{Body: map[string]any{"label": s.label.Load()}}, nil
}

type exampleDirectory struct{ *rte.Directory }

func (d exampleDirectory) ComponentConfigurations() ([]rte.Configuration, bool) {
	return d.Configurations(context.Background()), true
}

func (d exampleDirectory) SaveComponentConfiguration(ctx context.Context, id string, values map[string]any) error {
	return d.Patch(ctx, id, values)
}

func TestDeclaredFeatureUsesGenericAuthenticatedRoutesPagesAndConfiguration(t *testing.T) {
	initWebUITestConfig(t)
	ctx := context.Background()
	definition := rte.Definition{Scope: rte.AccountScope, Factory: func() rte.Component { return &exampleFeature{} }, Manifest: manifest.Manifest{
		ID: exampleID, Title: "Example",
		Config:   []manifest.ConfigField{manifest.Text("label", "Label", "original", false, false)},
		Provides: []manifest.Port{manifest.PortOf[ports.WebAction](exampleAction, 1, 0)},
		Pages:    []manifest.Page{{Path: "/example", Title: "Example", View: "showcase", Module: "/static/js/example.js"}},
	}, Routes: []types.WebRoute{{Path: "/api/example", Port: exampleAction}}, Assets: fstest.MapFS{
		"views/showcase.html":  &fstest.MapFile{Data: []byte(`<section id="view-showcase">Example page</section>`)},
		"static/js/example.js": &fstest.MapFile{Data: []byte(`export const page = {};`)},
	}}
	base, err := application.Catalog()
	require.NoError(t, err)
	catalog, err := rte.NewCatalog(append(base.Definitions(), definition)...)
	require.NoError(t, err)
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(definition.Manifest, definition.Factory))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	directory := exampleDirectory{rte.NewDirectory(catalog, configtest.NewStore())}
	require.NoError(t, directory.Bind("features", func() *rte.Runtime { return host }))
	server := NewServer(Options{Catalog: catalog, ComponentManager: directory})
	server.sessions["example-session"] = time.Now().Add(time.Hour)
	handler := server.routes()
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			req.AddCookie(&http.Cookie{Name: webUICookieName, Value: "example-session"})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	require.Equal(t, http.StatusUnauthorized, request(http.MethodGet, "/api/example", "", false).Code)
	for _, path := range []string{"/example", "/views/showcase.html", "/static/js/example.js"} {
		require.Equal(t, http.StatusOK, request(http.MethodGet, path, "", true).Code, path)
	}
	require.Contains(t, request(http.MethodGet, "/api/components", "", true).Body.String(), `"module":"/static/js/example.js"`)
	require.Contains(t, request(http.MethodGet, "/api/example", "", true).Body.String(), "original")
	require.Equal(t, http.StatusBadRequest, request(http.MethodPost, "/api/example", "broken", true).Code)
	require.Equal(t, http.StatusOK, request(http.MethodPatch, "/api/components", `{"id":"example.feature","values":{"label":"changed"}}`, true).Code)
	require.Contains(t, request(http.MethodGet, "/api/example", "", true).Body.String(), "changed")
	require.NoError(t, directory.SetEnabled(ctx, exampleID, false))
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodGet, "/api/example", "", true).Code)
	require.Error(t, func() error { _, err := directory.ResolveComponentPort("panel.webui", exampleAction); return err }())
}
