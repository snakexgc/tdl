package bot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/netutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
)

var processRebootRequested atomic.Bool

type Options struct {
	Token            string
	AllowedUsers     []int64
	Proxy            string
	Namespace        string
	NTP              string
	ReconnectTimeout time.Duration
	Watch            watch.Options
}

func RebootRequested() bool {
	return processRebootRequested.Load()
}

func Run(ctx context.Context, opts Options) (rerr error) {
	if opts.Token == "" {
		return errors.New("bot token is empty, please set bot.token in config.json")
	}

	// create telego bot with proxy
	bot, botLogger, err := newBot(opts.Token, opts.Proxy)
	if err != nil {
		return errors.Wrap(err, "create bot")
	}

	// verify bot identity
	botUser, err := bot.GetMe(ctx)
	if err != nil {
		return errors.Wrap(err, "get bot info")
	}
	color.Green("🤖 Bot @%s (ID: %d) started", botUser.Username, botUser.ID)
	notifier := newBotNotifier(bot, opts.AllowedUsers)
	allowed := newAllowedUsers(opts.AllowedUsers)

	if err := configureBotMenu(ctx, bot); err != nil {
		return errors.Wrap(err, "create bot menu")
	}

	kvEngine := kv.From(ctx)
	kvd, err := kvEngine.Open(opts.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}
	sessionOpts := login.SessionOptions{
		KV:               kvd,
		Proxy:            opts.Proxy,
		NTP:              opts.NTP,
		ReconnectTimeout: opts.ReconnectTimeout,
	}
	watchCtrl := newWatchController(ctx, opts.Watch, notifier.Notify)
	loginMgr := newLoginManager(ctx, bot, gotdLoginRunner{
		opts: sessionOpts,
	})
	aria2Factory := func() *watch.Aria2Controller {
		return watch.NewAria2Controller(config.Get(), kvd, nil)
	}
	loginMgr.SetOnSuccess(func(_ *tg.User) {
		notifyWatchAfterLogin(ctx, notifier, watchCtrl)
		go notifyAria2RetryCandidates(ctx, notifier, aria2Factory)
	})

	startup := checkSessionAndMaybeStartWatch(ctx, watchCtrl, sessionOpts)
	notifier.Notify(ctx, startupMessage(botUser, startup))
	if startup.WatchStarted {
		go notifyAria2RetryCandidates(ctx, notifier, aria2Factory)
	}

	// start long polling
	pollingCtx, cancelPolling := context.WithCancel(context.Background())
	defer cancelPolling()
	updates, err := bot.UpdatesViaLongPolling(pollingCtx, &telego.GetUpdatesParams{
		Timeout: 10,
	})
	if err != nil {
		return errors.Wrap(err, "start long polling")
	}

	// create bot handler
	bh, err := th.NewBotHandler(bot, updates)
	if err != nil {
		botLogger.SetShuttingDown()
		cancelPolling()
		return errors.Wrap(err, "create bot handler")
	}

	var rebootRequested atomic.Bool
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			botLogger.SetShuttingDown()
			watchCtrl.Stop()

			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := bh.StopWithContext(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
				color.Yellow("⚠️ Stop bot handler: %v", err)
			}
			cancelPolling()
		})
	}
	requestReboot := func() {
		rebootRequested.Store(true)
		processRebootRequested.Store(true)
		go shutdown()
	}
	afterConfigSave := func(cfg *config.Config) {
		allowed.Replace(cfg.Bot.AllowedUsers)
		notifier.UpdateChatIDs(cfg.Bot.AllowedUsers)
		watchCtrl.UpdateOptions(watch.DefaultOptions(cfg))
	}

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.Message == nil || update.Message.From == nil {
			return nil
		}

		fromID := update.Message.From.ID
		chatID := update.Message.Chat.ID

		// check if user is allowed
		if allowed.Contains(fromID) {
			return handleAllowedMessage(ctx, update.Message, loginMgr, afterConfigSave, requestReboot, aria2Factory, kvEngine, opts.Namespace, kvd)
		}

		// unauthorized user: reply with their ID as copyable text
		userIDStr := strconv.FormatInt(fromID, 10)
		color.Yellow("🚫 Unauthorized user %d, replying with ID", fromID)

		_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
			tu.ID(chatID),
			fmt.Sprintf("您的用户 ID 为：\n`%s`\n\n点击 ID 即可复制", userIDStr),
		).WithParseMode(telego.ModeMarkdownV2).WithReplyParameters(&telego.ReplyParameters{
			MessageID: update.Message.MessageID,
		}))

		return nil
	}, th.AnyMessage())

	color.Green("🔄 Bot is running... Press Ctrl+C to stop")

	go func() {
		<-ctx.Done()
		shutdown()
	}()

	err = bh.Start()
	botLogger.SetShuttingDown()
	cancelPolling()
	if rebootRequested.Load() {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

type startupState struct {
	Session      string
	Watch        string
	Hint         string
	WatchStarted bool
}

func startupMessage(botUser *telego.User, state startupState) string {
	botName := "(unknown)"
	if botUser != nil && botUser.Username != "" {
		botName = "@" + botUser.Username
	} else if botUser != nil {
		botName = fmt.Sprintf("Bot %d", botUser.ID)
	}

	parts := []string{
		fmt.Sprintf("您的TDL机器人 %s 已启动！", botName),
		"",
		versionSummary(),
		"",
		"MTProto session: " + state.Session,
		"watch: " + state.Watch,
	}
	if state.Hint != "" {
		parts = append(parts, "", state.Hint)
	}
	return strings.Join(parts, "\n")
}

func versionSummary() string {
	return fmt.Sprintf("Version: %s\nCommit: %s\nDate: %s\nGo: %s\nPlatform: %s/%s",
		consts.Version,
		consts.Commit,
		consts.CommitDate,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
}

func configureBotMenu(ctx context.Context, bot *telego.Bot) error {
	return bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "login_code", Description: "验证码登录"},
			{Command: "login_qr", Description: "扫描二维码登录"},
			{Command: "cancel_login", Description: "取消正在进行的登录"},
			{Command: "aria2_overview", Description: "查看下载任务概况"},
			{Command: "aria2_pause_all", Description: "暂停全部下载任务"},
			{Command: "aria2_start_all", Description: "开始全部下载任务"},
			{Command: "aria2_retry", Description: "重试已停止的下载任务"},
			{Command: "config", Description: "查看配置命令"},
			{Command: "config_get", Description: "查看全部配置"},
			{Command: "config_set", Description: "修改配置"},
			{Command: "reboot", Description: "重启(不推荐)"},
			{Command: "clean_kv", Description: "清空KV缓存(危险)"},
		},
	})
}

func checkSessionAndMaybeStartWatch(ctx context.Context, watchCtrl *watchController, opts login.SessionOptions) startupState {
	user, err := login.CheckSession(ctx, opts)
	switch {
	case err == nil:
		watchStatus := "已进入监听，正在等待表情触发。"
		if !watchCtrl.Start() {
			watchStatus = "已在运行。"
		}
		return startupState{
			Session:      "有效。" + login.UserSummary(user),
			Watch:        watchStatus,
			WatchStarted: true,
		}
	case errors.Is(err, login.ErrSessionUnauthorized):
		return startupState{
			Session: "无效或不存在。",
			Watch:   "未启动。",
			Hint:    "请使用 /login_code 或 /login_qr 登录；登录成功后会自动重新启动 watch。",
		}
	default:
		return startupState{
			Session: fmt.Sprintf("检查失败：%v", err),
			Watch:   "未启动。",
			Hint:    "请稍后重试，或使用 /login_code 或 /login_qr 重新登录；登录成功后会自动重新启动 watch。",
		}
	}
}

func notifyWatchAfterLogin(ctx context.Context, notifier *botNotifier, watchCtrl *watchController) {
	if watchCtrl.Start() {
		notifier.Notify(ctx, "登录完成，watch 已重新启动，正在监听表情触发。")
		return
	}
	notifier.Notify(ctx, "登录完成，watch 已在运行。")
}

func handleAllowedMessage(
	ctx *th.Context,
	msg *telego.Message,
	loginMgr *loginManager,
	afterConfigSave func(*config.Config),
	requestReboot func(),
	aria2Factory aria2ControllerFactory,
	kvEngine kv.Storage,
	namespace string,
	namespaceKV storage.Storage,
) error {
	fromID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if msg.Chat.Type != telego.ChatTypePrivate {
		if isPrivateCommand(text) {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
				tu.ID(chatID),
				"请在私聊中发送控制命令。",
			).WithReplyParameters(&telego.ReplyParameters{MessageID: msg.MessageID}))
		}
		return nil
	}

	if handled, err := handleConfigCommand(ctx, msg, text, afterConfigSave); handled || err != nil {
		return err
	}
	if handled, err := handleAria2Command(ctx, msg, text, aria2Factory); handled || err != nil {
		return err
	}
	if handled, err := handleKVCommand(ctx, msg, text, kvEngine, namespace, namespaceKV); handled || err != nil {
		return err
	}

	switch commandName(text) {
	case "/login_code":
		if err := loginMgr.StartCode(fromID, chatID); err != nil {
			if errors.Is(err, errLoginBusy) {
				_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
					tu.ID(chatID),
					"已有登录流程正在进行，请先完成或发送 /cancel_login 取消。",
				))
				return nil
			}
			return err
		}
		return nil
	case "/login_qr":
		if err := loginMgr.StartQR(fromID, chatID); err != nil {
			if errors.Is(err, errLoginBusy) {
				_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
					tu.ID(chatID),
					"已有登录流程正在进行，请先完成或发送 /cancel_login 取消。",
				))
				return nil
			}
			return err
		}
		return nil
	case "/cancel_login":
		if loginMgr.Cancel(fromID, chatID) {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "正在取消当前登录流程。"))
			return nil
		}
		if loginMgr.Busy() {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
				tu.ID(chatID),
				"已有登录流程正在进行，只能由发起会话取消。",
			))
			return nil
		}
		_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "当前没有可取消的登录流程。"))
		return nil
	case "/reboot":
		_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "正在重启程序，稍后会收到新的启动状态。"))
		if requestReboot != nil {
			requestReboot()
		}
		return nil
	}

	if loginMgr.HandleInput(fromID, chatID, text, msg.MessageID) {
		return nil
	}

	color.Cyan("✅ Allowed user %d sent message", fromID)
	return nil
}

func commandName(text string) string {
	cmd, _, _ := tu.ParseCommand(text)
	if cmd == "" {
		return ""
	}
	return "/" + cmd
}

func isPrivateCommand(text string) bool {
	switch commandName(text) {
	case "/login_code", "/login_qr", "/cancel_login", "/config", "/config_help", "/config_get", "/config_set", "/reboot",
		"/aria2", "/aria2_help", "/aria2_overview", "/aria2_pause_all", "/aria2_start_all", "/aria2_retry",
		"/clean_kv":
		return true
	default:
		return false
	}
}

// newBot creates a telego Bot instance with optional proxy support.
func newBot(token, proxyURL string) (*telego.Bot, *shutdownAwareTelegoLogger, error) {
	logger := newShutdownAwareTelegoLogger(token)
	opts := []telego.BotOption{
		telego.WithLogger(logger),
	}

	if proxyURL != "" {
		httpClient, err := newHTTPClientWithProxy(proxyURL)
		if err != nil {
			return nil, nil, errors.Wrap(err, "create http client with proxy")
		}
		opts = append(opts, telego.WithHTTPClient(httpClient))
	}

	bot, err := telego.NewBot(token, opts...)
	if err != nil {
		return nil, nil, errors.Wrap(err, "create telego bot")
	}

	return bot, logger, nil
}

// newHTTPClientWithProxy creates an *http.Client with proxy configured.
// Supports HTTP/HTTPS and SOCKS5 proxies.
func newHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, errors.Wrap(err, "parse proxy url")
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	switch u.Scheme {
	case "http", "https":
		// HTTP/HTTPS proxy: use Transport.Proxy
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		// SOCKS5 proxy: use netutil.NewProxy to get a ContextDialer
		dialer, err := netutil.NewProxy(proxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "create socks5 dialer")
		}
		transport.DialContext = dialer.DialContext
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s (supported: http, https, socks5, socks5h)", u.Scheme)
	}

	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}, nil
}
