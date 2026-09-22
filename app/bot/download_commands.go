package bot

import (
	"context"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/snakexgc/tdl/pkg/config"
)

const (
	botCmdStart   = "/start"
	botCmdMenu    = "/menu"
	botCmdHelp    = "/help"
	botCmdInfo    = "/info"
	botCmdForward = "/forward"

	botCmdDownloads         = "/downloads"
	botCmdDownloadsActive   = "/downloads_active"
	botCmdDownloadsWaiting  = "/downloads_waiting"
	botCmdDownloadsStopped  = "/downloads_stopped"
	botCmdDownloadsOverview = "/downloads_overview"
	botCmdDownloadsPauseAll = "/downloads_pause_all"
	botCmdDownloadsStartAll = "/downloads_start_all"

	botCmdAria2Retry = "/aria2_retry"
)

func handleDownloadCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	aria2Factory aria2ControllerFactory,
	localFactory localDownloadControllerFactory,
) (bool, error) {
	if commandName(text) == botCmdAria2Retry {
		return handleAria2Command(ctx, msg, text, aria2Factory)
	}
	if config.PrimaryDownloadExecutor(botConfiguration(ctx)) == config.DownloadExecutorLocal {
		return handleLocalDownloadCommand(ctx, msg, text, localFactory)
	}
	return handleAria2Command(ctx, msg, text, aria2Factory)
}

func handleDownloadCallback(
	ctx *th.Context,
	query telego.CallbackQuery,
	aria2Factory aria2ControllerFactory,
	localFactory localDownloadControllerFactory,
) error {
	switch {
	case strings.HasPrefix(query.Data, "aria2:"):
		return handleAria2Callback(ctx, query, aria2Factory)
	case strings.HasPrefix(query.Data, "local:"):
		return handleLocalDownloadCallback(ctx, query, localFactory)
	default:
		return nil
	}
}

func aria2DownloaderEnabled(contexts ...context.Context) bool {
	var ctx context.Context
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	cfg := config.From(ctx)
	return cfg != nil && cfg.Modules.Aria2 && config.UsesDownloadExecutor(cfg, config.DownloadExecutorAria2)
}

func botConfiguration(ctx *th.Context) *config.Config {
	if ctx == nil {
		return config.Get()
	}
	return config.From(ctx)
}
