package cmd

import (
	"os"
	"path/filepath"

	"github.com/go-faster/errors"
	"github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/app/bot"
	tdlruntime "github.com/snakexgc/tdl/app/runtime"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/util/fsutil"
	"github.com/snakexgc/tdl/internal/core/util/logutil"
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
	// allow PersistentPreRun to be called for every command
	cobra.EnableTraverseRunHooks = true
	cobra.MousetrapHelpText = ""

	cmd := &cobra.Command{
		Use:           "tdl",
		Short:         "Telegram Downloader, but more than a downloader",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBot(cmd)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == migrateConfigCommand {
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
			if cmd.Name() == migrateConfigCommand {
				return nil
			}
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

	cmd.Flags().String("component-config", "", "directory for component configuration")
	cmd.AddCommand(NewVersion(), NewMigrateConfig())

	return cmd
}

func runBot(cmd *cobra.Command) error {
	directory, err := cmd.Flags().GetString("component-config")
	if err != nil {
		return err
	}
	if directory == "" {
		if err := ensureStartupNTP(cmd.Context()); err != nil {
			return err
		}
	}
	return tdlruntime.Run(cmd.Context(), tdlruntime.Options{
		ComponentConfigDir: directory,
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
