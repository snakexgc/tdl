package panel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "panel.webui"

// Options binds protocol handlers; the component owns their serving lifecycle.
type Options struct {
	Configuration   ports.Configuration
	Sessions        ports.SessionCatalog
	DownloadLinks   ports.DownloadLinks
	DownloadCatalog ports.DownloadCatalog
	Login           ports.AccountLogin
	Address         string
	Handler         http.Handler
	Sync            func(context.Context) error
	Completed       chan<- error
}

func Register(registry *rte.Registry, opts Options) error {
	if opts.Handler == nil || opts.Address == "" {
		return errors.New("panel requires an address and handler")
	}
	var requires []manifest.Require
	if opts.Login != nil {
		requires = []manifest.Require{{Port: manifest.PortOf[ports.AccountLogin](ports.AccountLoginName, 1, 0)}}
	}
	var provides []manifest.Port
	if opts.DownloadCatalog != nil {
		provides = append(provides, manifest.PortOf[ports.DownloadCatalog](ports.DownloadCatalogName, 1, 0))
	}
	if opts.DownloadLinks != nil {
		provides = append(provides, manifest.PortOf[ports.DownloadLinks](ports.DownloadLinksName, 1, 0))
	}
	if opts.Sessions != nil {
		provides = append(provides, manifest.PortOf[ports.SessionCatalog](ports.SessionCatalogName, 1, 0))
	}
	if opts.Configuration != nil {
		provides = append(provides, manifest.PortOf[ports.Configuration](ports.ConfigurationName, 1, 0))
	}
	m := Manifest()
	m.Provides, m.Requires = provides, requires
	return registry.Register(m, func() rte.Component { return &service{opts: opts} })
}

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "panel", Title: "面板与数据维护", Order: 70, SettingsURL: "/config?tab=panel"}, ID: ID, Config: []manifest.ConfigField{manifest.Text("address", "监听地址", "0.0.0.0", false, true), manifest.Number("port", "监听端口", 22335, 1, 65535, true), manifest.FormattedText("username", "登录用户名", "admin", "nonempty", false, true), manifest.Text("password", "登录密码", "admin", true, true)}, Title: "Web 管理面板", Pages: []manifest.Page{
			{Path: "/dashboard", Title: "总览", View: "dashboard", Module: "/static/js/dashboard.js", Style: "/static/css/dashboard.css", Order: 10},
			{Path: "/config", Title: "设置", View: "config", Module: "/static/js/config.js", Style: "/static/css/config.css", Order: 60},
			{Path: "/kv", Title: "KV 管理", View: "kv", Module: "/static/js/kv.js", Style: "/static/css/kv.css", Order: 60, NavHidden: true, RedirectTo: "/downloads?tab=links"},
			{Path: "/modules", Title: "模块管理", View: "modules", Module: "/static/js/modules.js", Style: "/static/css/modules.css", Order: 50},
			{Path: "/components.html", Title: "组件配置", Order: 75, NavHidden: true, RedirectTo: "/config?tab=system#advanced"},
		},
	}, "panel", "面板访问")
}

type service struct {
	opts      Options
	runnables *schedule.Group
	listener  net.Listener
}

func (s *service) Init(_ context.Context, k rte.Kernel) error {
	s.runnables = k.Runnables
	if s.opts.DownloadCatalog != nil {
		if err := k.Provide(ports.DownloadCatalogName, s.opts.DownloadCatalog); err != nil {
			return err
		}
	}
	if s.opts.DownloadLinks != nil {
		if err := k.Provide(ports.DownloadLinksName, s.opts.DownloadLinks); err != nil {
			return err
		}
	}
	if s.opts.Sessions != nil {
		if err := k.Provide(ports.SessionCatalogName, s.opts.Sessions); err != nil {
			return err
		}
	}
	if s.opts.Configuration != nil {
		return k.Provide(ports.ConfigurationName, s.opts.Configuration)
	}
	return nil
}

func (s *service) Start(context.Context) error {
	listener, err := net.Listen("tcp", s.opts.Address)
	if err != nil {
		return err
	}
	s.listener = listener
	if err := s.runnables.Run("http.serve", 0, 0, s.serve, nil); err != nil {
		return err
	}
	if s.opts.Sync != nil {
		return s.runnables.Run("task.status.sync", 0, time.Minute, s.opts.Sync, nil)
	}
	return nil
}

func (s *service) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler: s.opts.Handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		// The RTE stop deadline is enforced by the owner. Do not report this
		// runnable finished while request handlers still use account resources.
		_ = server.Shutdown(context.Background())
	}()
	err := server.Serve(s.listener)
	cancel()
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if s.opts.Completed != nil {
		select {
		case s.opts.Completed <- err:
		default:
		}
	}
	return err
}

func (s *service) Stop(context.Context) error {
	if s.listener != nil {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}
func (*service) Reconfigure(context.Context, config.View) error { return nil }
