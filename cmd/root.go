package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/core/util/logutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
)

var (
	defaultBoltPath = filepath.Join(consts.DataDir, "data")

	DefaultLegacyStorage = map[string]string{
		kv.DriverTypeKey: kv.DriverLegacy.String(),
		"path":           filepath.Join(consts.DataDir, "data.kv"),
	}
	DefaultBoltStorage = map[string]string{
		kv.DriverTypeKey: kv.DriverBolt.String(),
		"path":           defaultBoltPath,
	}
)

// command groups
var (
	groupAccount = &cobra.Group{
		ID:    "account",
		Title: "Account related",
	}
	groupTools = &cobra.Group{
		ID:    "tools",
		Title: "Tools",
	}
)

func New() *cobra.Command {
	// allow PersistentPreRun to be called for every command
	cobra.EnableTraverseRunHooks = true

	// 获取可执行文件所在目录
	execPath, err := os.Executable()
	if err != nil {
		panic(errors.Wrap(err, "get executable path"))
	}
	execDir := filepath.Dir(execPath)

	// 初始化 JSON 配置
	if err := config.Init(execDir); err != nil {
		panic(errors.Wrap(err, "init config"))
	}

	cfg := config.Get()

	cmd := &cobra.Command{
		Use:           "tdl",
		Short:         "Telegram Downloader, but more than a downloader",
		SilenceErrors: true,
		SilenceUsage:  true,
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

			// v0.14.0: default storage changed from legacy to bolt, so we need to auto migrate to keep compatibility
			if !cmd.Flags().Lookup(consts.FlagStorage).Changed && !fsutil.PathExists(defaultBoltPath) {
				if err := migrateLegacyToBolt(); err != nil {
					return errors.Wrap(err, "migrate legacy to bolt")
				}
			}

			stg, err := kv.NewWithMap(getStorageConfig(cfg))
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

	cmd.AddGroup(groupAccount, groupTools)

	cmd.AddCommand(NewVersion(), NewLogin(), NewWatch())

	// 从 JSON 配置设置默认值
	cmd.PersistentFlags().StringToString(consts.FlagStorage,
		getStorageConfig(cfg),
		fmt.Sprintf("storage options, format: type=driver,key1=value1,key2=value2. Available drivers: [%s]",
			strings.Join(kv.DriverNames(), ",")))

	cmd.PersistentFlags().String(consts.FlagProxy, cfg.Proxy, "proxy address, format: protocol://username:password@host:port")
	cmd.PersistentFlags().StringP(consts.FlagNamespace, "n", cfg.Namespace, "namespace for Telegram session")
	cmd.PersistentFlags().Bool(consts.FlagDebug, cfg.Debug, "enable debug mode")

	cmd.PersistentFlags().IntP(consts.FlagPartSize, "s", 512*1024, "part size for transfer")
	_ = cmd.PersistentFlags().MarkDeprecated(consts.FlagPartSize, "part size has been set to maximum by default, this flag will be removed in the future")

	cmd.PersistentFlags().IntP(consts.FlagThreads, "t", cfg.Threads, "max threads for transfer one item")
	cmd.PersistentFlags().IntP(consts.FlagLimit, "l", cfg.Limit, "max number of concurrent tasks")
	cmd.PersistentFlags().Int(consts.FlagPoolSize, cfg.PoolSize, "specify the size of the DC pool, zero means infinity")
	cmd.PersistentFlags().Duration(consts.FlagDelay, time.Duration(cfg.Delay)*time.Second, "delay between each task, zero means no delay")

	cmd.PersistentFlags().String(consts.FlagNTP, cfg.NTP, "ntp server host, if not set, use system time")
	cmd.PersistentFlags().Duration(consts.FlagReconnectTimeout, time.Duration(cfg.ReconnectTimeout)*time.Second, "Telegram client reconnection backoff timeout, infinite if set to 0")

	// completion
	_ = cmd.RegisterFlagCompletionFunc(consts.FlagNamespace, func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		engine := kv.From(cmd.Context())
		ns, err := engine.Namespaces()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return ns, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// getStorageConfig 从配置获取存储配置
func getStorageConfig(cfg *config.Config) map[string]string {
	if cfg.Storage == nil {
		return DefaultBoltStorage
	}

	// 如果配置中指定了完整路径，使用它；否则相对于数据目录
	path := cfg.Storage["path"]
	if path == "" {
		path = defaultBoltPath
	} else if !filepath.IsAbs(path) {
		// 相对路径，转换为绝对路径
		path = filepath.Join(consts.DataDir, path)
	}

	// 确保存储类型有默认值
	storageType := cfg.Storage["type"]
	if storageType == "" {
		storageType = "bolt"
	}

	return map[string]string{
		kv.DriverTypeKey: storageType,
		"path":           path,
	}
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
