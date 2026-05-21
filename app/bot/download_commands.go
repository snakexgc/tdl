package bot

import (
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/iyear/tdl/pkg/config"
)

const (
	botCmdStart   = "/start"
	botCmdMenu    = "/menu"
	botCmdHelp    = "/help"
	botCmdInfo    = "/info"
	botCmdWeb     = "/web"
	botCmdPath    = "/path"
	botCmdForward = "/forward"

	botCmdDownloads         = "/downloads"
	botCmdDownloadsHelp     = "/downloads_help"
	botCmdDownloadsActive   = "/downloads_active"
	botCmdDownloadsWaiting  = "/downloads_waiting"
	botCmdDownloadsStopped  = "/downloads_stopped"
	botCmdDownloadsOverview = "/downloads_overview"
	botCmdDownloadsPauseAll = "/downloads_pause_all"
	botCmdDownloadsStartAll = "/downloads_start_all"

	botCmdAria2         = "/aria2"
	botCmdAria2Help     = "/aria2_help"
	botCmdAria2Active   = "/aria2_active"
	botCmdAria2Waiting  = "/aria2_waiting"
	botCmdAria2Stopped  = "/aria2_stopped"
	botCmdAria2Overview = "/aria2_overview"
	botCmdAria2PauseAll = "/aria2_pause_all"
	botCmdAria2StartAll = "/aria2_start_all"
	botCmdAria2Retry    = "/aria2_retry"

	botCmdInternal         = "/internal"
	botCmdInternalHelp     = "/internal_help"
	botCmdInternalActive   = "/internal_active"
	botCmdInternalWaiting  = "/internal_waiting"
	botCmdInternalStopped  = "/internal_stopped"
	botCmdInternalOverview = "/internal_overview"
	botCmdInternalPauseAll = "/internal_pause_all"
	botCmdInternalStartAll = "/internal_start_all"
)

func handleDownloadCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	aria2Factory aria2ControllerFactory,
	internalFactory internalDownloadControllerFactory,
) (bool, error) {
	if config.EffectiveDownloaderMode(config.Get()) == config.DownloaderModeInternal {
		return handleInternalDownloadCommand(ctx, msg, text, internalFactory)
	}
	return handleAria2Command(ctx, msg, text, aria2Factory)
}

func handleDownloadCallback(
	ctx *th.Context,
	query telego.CallbackQuery,
	aria2Factory aria2ControllerFactory,
	internalFactory internalDownloadControllerFactory,
) error {
	switch {
	case strings.HasPrefix(query.Data, "aria2:"):
		return handleAria2Callback(ctx, query, aria2Factory)
	case strings.HasPrefix(query.Data, "internal:"):
		return handleInternalDownloadCallback(ctx, query, internalFactory)
	default:
		return nil
	}
}

func aria2DownloaderEnabled() bool {
	return config.EffectiveDownloaderMode(config.Get()) == config.DownloaderModeAria2
}
