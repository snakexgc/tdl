package watch

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/pkg/config"
)

type Options struct {
	Dir                     string
	Template                string
	FilenameMaxLength       int
	SkipSame                bool
	PoolSize                int
	Limit                   int
	Download                bool
	TriggerReactions        []string
	Include                 []string
	Exclude                 []string
	FileSizeMB              int64
	Forward                 bool
	ForwardMode             string
	ForwardTarget           string
	ForwardListen           []string
	ForwardListenComments   bool
	ForwardSilent           bool
	ForwardDedupeTTL        time.Duration
	ForwardTriggerReactions []string
	Notify                  NotifyFunc
	messageLinks            <-chan messageLinkSubmission
}

type NotifyFunc func(ctx context.Context, text string)

func DefaultOptions(cfg *config.Config) Options {
	if cfg == nil {
		cfg = config.Get()
	}

	return Options{
		Dir:                     cfg.DownloadDir,
		Template:                fileNameConfigTemplate(config.EffectiveFilename(cfg)),
		FilenameMaxLength:       config.EffectiveFilenameMax(cfg),
		PoolSize:                config.EffectivePoolSize(cfg),
		Limit:                   config.EffectiveLimit(cfg),
		Download:                cfg.Modules.Watch,
		TriggerReactions:        append([]string(nil), cfg.TriggerReactions...),
		Include:                 append([]string(nil), cfg.Include...),
		Exclude:                 append([]string(nil), cfg.Exclude...),
		FileSizeMB:              cfg.FileSizeMB,
		Forward:                 cfg.Modules.Forward,
		ForwardMode:             config.EffectiveForwardMode(cfg),
		ForwardTarget:           cfg.Forward.Target,
		ForwardListen:           append([]string(nil), cfg.Forward.Listen...),
		ForwardListenComments:   cfg.Forward.ListenComments,
		ForwardSilent:           cfg.Forward.Silent,
		ForwardDedupeTTL:        time.Duration(config.EffectiveForwardDedupeTTL(cfg)) * time.Second,
		ForwardTriggerReactions: append([]string(nil), cfg.Forward.TriggerReactions...),
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
