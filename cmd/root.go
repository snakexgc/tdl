package cmd

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/go-faster/errors"
	"github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/snakexgc/tdl/app/bot"
	"github.com/snakexgc/tdl/app/reset"
	tdlruntime "github.com/snakexgc/tdl/app/runtime"
	"github.com/snakexgc/tdl/application"
	configuration "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/interfaces/types"
	bootstrapconfig "github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/util/logutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/kv"
)

type (
	startupConfigurationKey struct{}
	startupConfiguration    struct {
		service *configuration.Service
	}
)

var openStorage = func() (kv.Storage, error) { return kv.New(kv.DriverBolt, consts.DataDir) }

func New() *cobra.Command {
	var closeLog func() error
	var closeStorage func() error
	cleanup := func() error {
		var err error
		if closeStorage != nil {
			err = closeStorage()
			closeStorage = nil
			if err != nil {
				slog.Error("关闭存储失败", "component", "system", "error", err)
			}
		}
		if closeLog != nil {
			err = multierr.Combine(err, closeLog())
			closeLog = nil
		}
		return err
	}
	// allow PersistentPreRun to be called for every command
	cobra.EnableTraverseRunHooks = true
	cobra.MousetrapHelpText = ""

	cmd := &cobra.Command{
		Use:           "tdl",
		Short:         "Telegram Downloader, but more than a downloader",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runBot(cmd)
			if err != nil {
				level := zap.ErrorLevel
				message := "TDL 运行失败"
				if errors.Is(err, context.Canceled) {
					level = zap.DebugLevel
					message = "TDL 运行已取消"
				}
				logctx.From(cmd.Context()).Log(level, message, zap.Error(err))
			} else {
				logctx.From(cmd.Context()).Info("TDL 已停止")
			}
			// Cobra skips post-run hooks on errors, so close sinks here as well.
			return multierr.Combine(err, cleanup())
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) (runErr error) {
			defer func() {
				if runErr != nil && closeLog != nil {
					logctx.From(cmd.Context()).Error("TDL 初始化失败", zap.Error(runErr))
					runErr = multierr.Combine(runErr, cleanup())
				}
			}()
			if cmd.Name() == versionCommand || cmd.Name() == configInitCommand {
				return nil
			}
			if err := consts.InitPaths(); err != nil {
				return err
			}
			service, err := bootstrapconfig.Open(cmd.Context(), consts.HomeDir)
			if err != nil {
				return err
			}
			_, err = bootstrapconfig.Install(cmd.Context(), service)
			if err != nil {
				return err
			}
			cmd.SetContext(context.WithValue(cmd.Context(), startupConfigurationKey{}, startupConfiguration{service}))
			cfg := config.Get()
			// init logger
			level := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
				if config.Get().Debug {
					return level >= zap.DebugLevel
				}
				return level >= zap.InfoLevel
			})
			logger, logs, closeFile := logutil.NewSession(level, filepath.Join(consts.LogPath, "latest.log"), cfg.Namespace)
			previous := slog.Default()
			slog.SetDefault(logging.Slog(logger))
			closeLog = func() error {
				slog.SetDefault(previous)
				return multierr.Combine(logger.Sync(), closeFile())
			}
			cmd.SetContext(logging.WithStore(logctx.With(cmd.Context(), logger), logs))

			logger.Info("TDL 正在启动", zap.Bool("debug_enabled", cfg.Debug))

			stg, err := openStorage()
			if err != nil {
				return errors.Wrap(err, "create kv storage")
			}

			cmd.SetContext(kv.With(cmd.Context(), stg))
			closeStorage = stg.Close

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == versionCommand || cmd.Name() == configInitCommand {
				return nil
			}
			return cleanup()
		},
	}

	coloredcobra.Init(&coloredcobra.Config{
		RootCmd:         cmd,
		Headings:        coloredcobra.HiCyan + coloredcobra.Bold + coloredcobra.Underline,
		Commands:        coloredcobra.HiGreen + coloredcobra.Bold,
		CmdShortDescr:   coloredcobra.None,
		ExecName:        coloredcobra.Bold,
		Flags:           coloredcobra.Bold + coloredcobra.Yellow,
		FlagsDataType:   coloredcobra.Blue,
		FlagsDescr:      coloredcobra.None,
		Aliases:         coloredcobra.None,
		Example:         coloredcobra.None,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})

	cmd.AddCommand(NewVersion(), NewConfigInit())

	return cmd
}

func runBot(cmd *cobra.Command) error {
	startup, ok := cmd.Context().Value(startupConfigurationKey{}).(startupConfiguration)
	if !ok {
		return errors.New("configuration manager is not initialized")
	}
	store, err := ensureStartupNTP(cmd.Context(), startup.service)
	if err != nil {
		return err
	}
	host, err := application.ConfigurationHost(cmd.Context(), types.AccountID(config.Get().Namespace), startup.service)
	if err != nil {
		return err
	}
	defer host.Stop(context.Background())
	logctx.From(cmd.Context()).Info("统一配置已加载", zap.String("component", configuration.ID), zap.String("file", filepath.Join(consts.HomeDir, configuration.Filename)))
	plan := reset.New(consts.HomeDir)
	return tdlruntime.Run(cmd.Context(), tdlruntime.Options{
		ComponentStore:    store,
		ConfigurationHost: host,
		ResetPlan:         plan,
		RequestReset:      func() { reset.Request(plan) },
		RequestReboot:     bot.RequestReboot,
		RequestUpdate:     bot.RequestUpdate,
	})
}
