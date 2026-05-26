package cmd

import (
	"os"
	"path/filepath"

	"github.com/go-faster/errors"
	"github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/bot"
	tdlruntime "github.com/iyear/tdl/app/runtime"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/core/util/logutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
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
	// allow PersistentPreRun to be called for every command
	cobra.EnableTraverseRunHooks = true
	cobra.MousetrapHelpText = ""

	// 初始化 JSON 配置
	if err := config.Init(consts.HomeDir); err != nil {
		panic(errors.Wrap(err, "init config"))
	}

	cfg := config.Get()

	cmd := &cobra.Command{
		Use:           "tdl",
		Short:         "Telegram Downloader, but more than a downloader",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBot(cmd)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// init logger
			debug, level := cfg.Debug, zap.InfoLevel
			if debug {
				level = zap.DebugLevel
			}
			cmd.SetContext(logctx.With(cmd.Context(),
				logutil.New(level, filepath.Join(consts.LogPath, "latest.log"))))

			ns := cfg.Namespace
			if ns != "" {
				logctx.From(cmd.Context()).Info("Namespace",
					zap.String("namespace", ns))
			}

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

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			return multierr.Combine(
				kv.From(cmd.Context()).Close(),
				logctx.From(cmd.Context()).Sync(),
			)
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

	cmd.AddCommand(NewVersion())

	return cmd
}

func runBot(cmd *cobra.Command) error {
	if err := ensureStartupNTP(cmd.Context()); err != nil {
		return err
	}
	return tdlruntime.Run(cmd.Context(), tdlruntime.Options{
		RequestReboot: bot.RequestReboot,
		RequestUpdate: bot.RequestUpdate,
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
