package messagelink

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "trigger.messagelink"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.Manifest{
		Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: "/config?tab=download"},
		ID:      ID, Title: "消息链接触发",
		Provides: []manifest.Port{manifest.PortOf[ports.MessageLinks](ports.MessageLinksName, 1, 0)},
	}, func() rte.Component { return &Validator{} })
}

type Validator struct{ account types.AccountID }

func (v *Validator) Init(_ context.Context, k rte.Kernel) error {
	v.account = k.Account
	return k.Provide(ports.MessageLinksName, v)
}
func (*Validator) Start(context.Context) error                    { return nil }
func (*Validator) Stop(context.Context) error                     { return nil }
func (*Validator) Reconfigure(context.Context, config.View) error { return nil }

func (v *Validator) Validate(ctx context.Context, account types.AccountID, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if account != v.account {
		return "", errors.New("message link account mismatch")
	}
	return ValidateTelegramMessageHTTPLink(raw)
}

// Submit orchestrates validation, protocol resolution and bounded submission.
// Source and requests are capabilities bound to the caller's live connection.
func (v *Validator) Submit(ctx context.Context, account types.AccountID, raw string, source ports.MessageLinkSource, requests ports.DownloadRequests) (types.DownloadSubmissionSummary, error) {
	link, err := v.Validate(ctx, account, raw)
	if err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	if source == nil || requests == nil {
		return types.DownloadSubmissionSummary{}, errors.New("message download resources are unavailable")
	}
	intent, err := source.Resolve(ctx, account, link)
	if err != nil {
		return types.DownloadSubmissionSummary{Link: link}, err
	}
	if intent.Account != account {
		return types.DownloadSubmissionSummary{}, errors.New("message source account mismatch")
	}
	intent.Link, intent.Source = link, "message_link"
	return requests.Submit(ctx, intent)
}
