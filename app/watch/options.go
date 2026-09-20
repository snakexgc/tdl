package watch

import (
	"context"

	appforward "github.com/snakexgc/tdl/app/forward"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

type Options struct {
	FeatureFlags      func() (download, forward bool)
	ForwardRules      ports.ForwardRules
	ForwardRouting    ports.ForwardRouting
	Connections       *tgauth.Connections
	ComponentStore    *rteconfig.Store
	SetIntentHost     func(*rte.Runtime)
	SetDownloadHost   func(*rte.Runtime)
	ForwardQueue      *appforward.Queue
	Account           types.AccountID
	Filter            ports.FilterRules
	Naming            ports.NamingRules
	Reaction          ports.ReactionTrigger
	MessageLinks      ports.MessageLinks
	Credentials       ports.TelegramCredentials
	DownloadRouting   ports.DownloadRouting
	DownloadPipeline  ports.DownloadPipeline
	Dir               string
	Template          string
	FilenameMaxLength int
	SkipSame          bool
	PoolSize          int
	Limit             int
	Download          bool
	Include           []string
	Exclude           []string
	FileSizeMinMB     int64
	FileSizeMaxMB     int64
	Forward           bool
	Notify            NotifyFunc
	HTTPService       *httpdl.Service
	DownloadSubmitter ports.DownloadExecutor
	messageLinks      <-chan messageLinkSubmission
}

func (w *Watcher) downloadEnabled() bool {
	if w.opts.FeatureFlags != nil {
		download, _ := w.opts.FeatureFlags()
		return download
	}
	return w.opts.Download
}

func (w *Watcher) forwardEnabled() bool {
	if w.opts.FeatureFlags != nil {
		_, forward := w.opts.FeatureFlags()
		return forward
	}
	return w.opts.Forward
}

type NotifyFunc func(ctx context.Context, text string)

func DefaultOptions(cfg *config.Config) Options {
	if cfg == nil {
		cfg = config.Get()
	}

	return Options{
		Account:           types.AccountID(cfg.Namespace),
		Dir:               cfg.DownloadDir,
		Template:          config.EffectiveFilename(cfg),
		FilenameMaxLength: config.EffectiveFilenameMax(cfg),
		PoolSize:          config.EffectivePoolSize(cfg),
		Limit:             config.EffectiveLimit(cfg),
		Download:          cfg.Modules.Watch,
		Include:           append([]string(nil), cfg.Include...),
		Exclude:           append([]string(nil), cfg.Exclude...),
		FileSizeMinMB:     cfg.FileSizeMinMB,
		FileSizeMaxMB:     cfg.FileSizeMaxMB,
		Forward:           cfg.Modules.Forward,
	}
}

func effectiveWatchOptionLimit(value int, cfg *config.Config) int {
	if value < 1 {
		return config.EffectiveLimit(cfg)
	}
	return value
}

func effectiveWatchOptionPoolSize(value int, cfg *config.Config) int {
	if value < 1 {
		return config.EffectivePoolSize(cfg)
	}
	return value
}
