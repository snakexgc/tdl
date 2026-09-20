package cmd

import (
	"context"
	"log/slog"
	"os"
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
	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/util/fsutil"
	"github.com/snakexgc/tdl/internal/core/util/logutil"
	"github.com/snakexgc/tdl/internal/migration"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/kv"
)

var (
	defaultBoltPath = consts.DataDir

	DefaultLegacyStorage = map[string]string{
		kv.DriverTypeKey: kv.DriverLegacy.String(),
		"path":           filepath.Join(consts.DataDir, "data.kv"),
	}
	DefaultBoltStorage = map[string]string{
		kv.DriverTypeKey: kv.DriverBolt.String(),
		"path":           defaultBoltPath,
	}
)

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
			if cmd.Name() == migrateConfigCommand || cmd.Name() == versionCommand {
				return nil
			}
			if err := consts.InitPaths(); err != nil {
				return err
			}
			if err := config.Init(consts.HomeDir); err != nil {
				return err
			}
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

			// v0.14.0: default storage changed from legacy to bolt, so we need to auto migrate to keep compatibility.
			if shouldMigrateLegacyToBolt() {
				if err := migrateLegacyToBolt(); err != nil {
					return errors.Wrap(err, "migrate legacy to bolt")
				}
			}

			stg, err := kv.NewWithMap(DefaultBoltStorage)
			if err != nil {
				return errors.Wrap(err, "create kv storage")
			}

			cmd.SetContext(kv.With(cmd.Context(), stg))
			closeStorage = stg.Close

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == migrateConfigCommand || cmd.Name() == versionCommand {
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

	cmd.Flags().String("component-config", "", "component configuration directory (default: import once under the application components directory)")
	cmd.AddCommand(NewVersion(), NewMigrateConfig())

	return cmd
}

func runBot(cmd *cobra.Command) error {
	directory, err := cmd.Flags().GetString("component-config")
	if err != nil {
		return err
	}
	if directory == "" {
		if _, statErr := os.Stat(migration.ComponentDirectory(consts.HomeDir, config.Get().Namespace)); os.IsNotExist(statErr) {
			if err := ensureStartupNTP(cmd.Context()); err != nil {
				return err
			}
		}
		directory, err = migration.EnsureComponents(cmd.Context(), consts.HomeDir, config.Get())
		if err != nil {
			return err
		}
	}
	logctx.From(cmd.Context()).Info("Component configuration", zap.String("directory", directory))
	plan := reset.New(consts.HomeDir, directory)
	return tdlruntime.Run(cmd.Context(), tdlruntime.Options{
		ComponentConfigDir: directory,
		ResetPlan:          plan,
		RequestReset:       func() { reset.Request(plan) },
		RequestReboot:      bot.RequestReboot,
		RequestUpdate:      bot.RequestUpdate,
	})
}

func shouldMigrateLegacyToBolt() bool {
	legacyPath := DefaultLegacyStorage["path"]
	if legacyPath == "" || !fsutil.PathExists(legacyPath) {
		return false
	}

	entries, err := os.ReadDir(defaultBoltPath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == filepath.Base(legacyPath) {
			continue
		}
		return false
	}
	return true
}

func migrateLegacyToBolt() (rerr error) {
	legacy, err := kv.NewWithMap(DefaultLegacyStorage)
	if err != nil {
		return errors.Wrap(err, "create legacy kv storage")
	}
	defer multierr.AppendInvoke(&rerr, multierr.Close(legacy))

	bolt, err := kv.NewWithMap(DefaultBoltStorage)
	if err != nil {
		return errors.Wrap(err, "create bolt kv storage")
	}
	defer multierr.AppendInvoke(&rerr, multierr.Close(bolt))

	meta, err := legacy.MigrateTo()
	if err != nil {
		return errors.Wrap(err, "migrate legacy to bolt")
	}

	return bolt.MigrateFrom(meta)
}
