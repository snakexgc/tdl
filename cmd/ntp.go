package cmd

import (
	"context"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	configuration "github.com/snakexgc/tdl/application/configuration.manager"
	bootstrapconfig "github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/internal/core/logctx"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func ensureStartupNTP(ctx context.Context, service *configuration.Service) (*rteconfig.Store, error) {
	selection, err := bootstrapconfig.SelectAndSaveStartupNTP(ctx, service)
	if err != nil {
		return nil, errors.Wrap(err, "select startup NTP server")
	}
	// Publish the saved value before runtime hosts freeze their configuration.
	store, err := bootstrapconfig.Install(ctx, service)
	if err != nil {
		return nil, err
	}
	logger := logctx.From(ctx).With(
		zap.String("component", accounttelegram.ID),
		zap.String("operation", "startup_ntp"),
		zap.String("source", selection.Source),
		zap.String("server", selection.Host),
		zap.Duration("elapsed", selection.Elapsed),
		zap.Bool("saved", selection.Saved),
	)
	switch {
	case selection.Host == "":
		logger.Warn("未找到可用的 NTP 服务器，使用系统时间")
	case selection.ConfiguredFailed:
		logger.Warn("已配置的 NTP 服务器不可用，已选择最快的可用内置服务器")
	default:
		logger.Info("NTP 服务器已选择")
	}
	return store, nil
}
