package bot

import (
	"context"
	"fmt"
	"log/slog"
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

	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/util/netutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/schedule"
)

var processRebootRequested atomic.Bool
var (
	processUpdateMu   sync.Mutex
	processUpdatePlan *types.UpdatePlan
)

type Options struct {
	CommandResolver       func(string, string) (any, error)
	CommandContributions  []ports.ConsoleContribution
	SessionChecker        ports.AccountSession
	Connections           *tgauth.Connections
	DownloadControl       ports.DownloadControl
	Credentials           ports.TelegramCredentials
	Updater               ports.Updater
	ComponentStore        *rteconfig.Store
	SetComponentHost      func(*rte.Runtime)
	SetComponentRefresh   func(func(context.Context) error)
	Token                 string
	AllowedUsers          []int64
	Proxy                 string
	Namespace             string
	NTP                   string
	ReconnectTimeout      time.Duration
	WatchControl          watchControl
	DisableAutoStartWatch bool
	OnLoginSuccess        func(*tg.User)
	SetNotifier           func(watch.NotifyFunc)
	RequestReboot         func()
	RequestUpdate         func(types.UpdatePlan)
}

type watchControl interface {
	Start() bool
	Stop()
	UpdateOptions(watch.Options)
	Running() bool
	SubmitMessageLink(context.Context, string) (watch.MessageLinkSubmissionResult, error)
}

func RebootRequested() bool {
	return processRebootRequested.Load()
}

func RequestReboot() {
	processRebootRequested.Store(true)
	processUpdateMu.Lock()
	processUpdatePlan = nil
	processUpdateMu.Unlock()
}

func UpdateRequested() (types.UpdatePlan, bool) {
	processUpdateMu.Lock()
	defer processUpdateMu.Unlock()
	if processUpdatePlan == nil {
		return types.UpdatePlan{}, false
	}
	return *processUpdatePlan, true
}

func RequestUpdate(plan types.UpdatePlan) {
	processRebootRequested.Store(false)
	processUpdateMu.Lock()
	processUpdatePlan = &plan
	processUpdateMu.Unlock()
}

func Run(ctx context.Context, opts Options) (rerr error) {
	if opts.Token == "" {
		return errors.New("bot token is empty, please set console.bot values.token for the selected account in tdl_config.json")
	}

	if opts.CommandResolver == nil || opts.DownloadControl == nil || opts.WatchControl == nil {
		return errors.New("bot requires runtime component ports")
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
	account := types.AccountID(opts.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	botLogger.logger = botLogger.logger.With("account", account)
	slog.Info("机器人身份验证成功", "component", "console.bot", "account", account, "bot_id", botUser.ID)
	color.Green("🤖 Bot @%s (ID: %d) started", botUser.Username, botUser.ID)
	transport := &botNotificationTransport{sender: bot, editor: bot}
	host, console, notifications, err := application.BotHost(ctx, account, transport, opts.AllowedUsers, opts.ComponentStore, opts.CommandContributions...)
	if err != nil {
		return errors.Wrap(err, "start bot components")
	}
	notifier := &botNotifier{host: host, service: notifications, account: account}
	defer notifier.Close()
	if opts.SetComponentHost != nil {
		opts.SetComponentHost(host)
		defer opts.SetComponentHost(nil)
	}
	if opts.SetNotifier != nil {
		opts.SetNotifier(notifier.Notify)
		defer opts.SetNotifier(nil)
	}

	if err := configureBotMenu(ctx, bot, console); err != nil {
		return errors.Wrap(err, "create bot menu")
	}
	menuChanged := make(chan struct{}, 1)
	background := schedule.New(ctx)
	defer func() { _ = background.Stop(context.Background()) }()
	if err := background.Run("console.menu", 0, 0, func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-menuChanged:
				bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := configureBotMenu(bounded, bot, console)
				cancel()
				if err != nil && ctx.Err() == nil {
					slog.Warn("刷新机器人菜单失败，稍后重试", "component", "console.bot", "account", account, "error", err)
					timer := time.NewTimer(5 * time.Second)
					select {
					case <-ctx.Done():
						timer.Stop()
						return nil
					case <-timer.C:
					}
					select {
					case menuChanged <- struct{}{}:
					default:
					}
				}
			}
		}
	}, nil); err != nil {
		return err
	}
	if opts.SetComponentRefresh != nil {
		var refreshMu sync.Mutex
		refresh := func(refreshCtx context.Context) error {
			refreshMu.Lock()
			defer refreshMu.Unlock()
			if err := application.ReconcileBotHost(refreshCtx, host, account, transport, opts.AllowedUsers, opts.ComponentStore, opts.CommandContributions...); err != nil {
				return err
			}
			select {
			case menuChanged <- struct{}{}:
			default:
			}
			return nil
		}
		opts.SetComponentRefresh(refresh)
		defer opts.SetComponentRefresh(nil)
		// Catch saves that completed between host creation and callback binding.
		if err := refresh(ctx); err != nil {
			return err
		}
	}

	kvEngine := kv.From(ctx)
	kvd, err := kvEngine.Open(opts.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}
	sessionOptionsForKV := func(kvd storage.Storage) login.SessionOptions {
		return login.SessionOptions{
			Connections: opts.Connections, Credentials: opts.Credentials, Account: account,
			KV:               kvd,
			Proxy:            opts.Proxy,
			NTP:              opts.NTP,
			ReconnectTimeout: opts.ReconnectTimeout,
		}
	}
	sessionOpts := sessionOptionsForKV(kvd)
	sessionOpts.Checker = opts.SessionChecker
	watchCtrl := opts.WatchControl
	loginMgr := newLoginManagerWithFactory(ctx, bot, func(namespace string) (loginRunner, error) {
		namespace, err := config.NormalizeNamespace(namespace)
		if err != nil {
			return nil, err
		}
		targetKV, err := kvEngine.Open(namespace)
		if err != nil {
			return nil, errors.Wrap(err, "open namespace storage")
		}
		targetOptions := sessionOptionsForKV(targetKV)
		targetOptions.Account = types.AccountID(namespace)
		return gotdLoginRunner{opts: targetOptions}, nil
	})
	aria2Factory := componentAria2Factory(console, opts.CommandResolver)
	downloadControl := opts.DownloadControl
	localFactory := func() *localDownloadControl {
		return &localDownloadControl{port: downloadControl, account: account}
	}
	if err := background.Run("aria2.events", 0, 0, func(ctx context.Context) error {
		runAria2EventListener(ctx, notifier, aria2Factory)
		return nil
	}, nil); err != nil {
		return err
	}
	retryCandidates := make(chan struct{}, 1)
	if err := background.Run("aria2.retry-candidates", 0, 0, func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-retryCandidates:
				notifyAria2RetryCandidates(ctx, notifier, aria2Factory)
			}
		}
	}, nil); err != nil {
		return err
	}
	requestRetryCandidates := func() {
		select {
		case retryCandidates <- struct{}{}:
		default:
		}
	}
	var requestReboot func()
	onLoginSuccess := func(flowCtx context.Context, user *tg.User, namespace string) {
		restart, err := config.SelectNamespace(flowCtx, string(account), namespace)
		if err != nil {
			notifier.Notify(ctx, fmt.Sprintf("登录成功，但保存用户配置失败：%v", err))
			return
		}
		if opts.OnLoginSuccess != nil {
			opts.OnLoginSuccess(user)
		}
		if restart {
			notifier.Notify(ctx, fmt.Sprintf("登录成功，正在重启以切换到用户 %s。", namespace))
			if requestReboot != nil {
				requestReboot()
			}
			return
		}
		if !opts.DisableAutoStartWatch {
			notifyWatchAfterLogin(ctx, notifier, watchCtrl)
		}
		if aria2DownloaderEnabled(ctx) {
			requestRetryCandidates()
		}
	}
	loginMgr.SetOnSuccess(func(flowCtx context.Context, user *ports.LoginUser, namespace string) {
		onLoginSuccess(flowCtx, &tg.User{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}, namespace)
	})
	loginHost, loginPort, err := application.BotLoginHost(ctx, account, loginMgr)
	if err != nil {
		return err
	}
	defer loginHost.Stop(context.Background())
	maintenanceHost, maintenancePort, err := application.MaintenanceHost(ctx, account, taskhub.CleanupRepository{Engine: kvEngine, Namespace: string(account), Store: kvd})
	if err != nil {
		return err
	}
	defer maintenanceHost.Stop(context.Background())

	startup := checkSessionAndMaybeStartWatch(ctx, watchCtrl, sessionOpts, !opts.DisableAutoStartWatch)
	notifier.Notify(ctx, startupMessage(botUser, startup))
	if startup.WatchStarted && aria2DownloaderEnabled(ctx) {
		requestRetryCandidates()
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

			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := bh.StopWithContext(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("停止机器人处理器失败", "component", "console.bot", "account", account, "error", err)
			}
			cancelPolling()
		})
	}
	shutdownRequests := make(chan struct{}, 1)
	if err := background.Run("console.shutdown", 0, 0, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
		case <-shutdownRequests:
		}
		shutdown()
		return nil
	}, nil); err != nil {
		return err
	}
	requestShutdown := func() {
		select {
		case shutdownRequests <- struct{}{}:
		default:
		}
	}
	requestReboot = func() {
		rebootRequested.Store(true)
		if opts.RequestReboot != nil {
			opts.RequestReboot()
		} else {
			RequestReboot()
		}
		requestShutdown()
	}
	requestUpdate := func(plan types.UpdatePlan) {
		rebootRequested.Store(false)
		if opts.RequestUpdate != nil {
			opts.RequestUpdate(plan)
		} else {
			RequestUpdate(plan)
		}
		requestShutdown()
	}
	updateController := newTDLUpdateController(requestUpdate)
	updateController.updater = opts.Updater
	botContext := ctx
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		ctx = ctx.WithContext(config.InheritSource(ctx, botContext))
		if !console.Allowed(account, query.From.ID) {
			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("没有权限。"))
			return nil
		}
		return handleDownloadCallback(ctx, query, aria2Factory, localFactory)
	}, th.AnyCallbackQuery())

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		ctx = ctx.WithContext(config.InheritSource(ctx, botContext))
		if update.Message == nil || update.Message.From == nil {
			return nil
		}

		fromID := update.Message.From.ID
		chatID := update.Message.Chat.ID

		// check if user is allowed
		if console.Allowed(account, fromID) {
			contributions := append(append([]ports.ConsoleContribution{}, opts.CommandContributions...), declaredCommandHandlers(console.Commands(), opts.CommandResolver)...)
			return handleAllowedMessage(ctx, update.Message, loginPort, requestReboot, updateController, watchCtrl, aria2Factory, localFactory, maintenancePort, account, console, contributions...)
		}

		// unauthorized user: reply with their ID as copyable text
		userIDStr := strconv.FormatInt(fromID, 10)
		slog.Warn("拒绝未授权的机器人用户", "component", "console.bot", "account", account, "user_id", fromID)

		_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
			tu.ID(chatID),
			fmt.Sprintf("您的用户 ID 为：\n`%s`", userIDStr),
		).WithParseMode(telego.ModeMarkdownV2).WithReplyParameters(&telego.ReplyParameters{
			MessageID: update.Message.MessageID,
		}))

		return nil
	}, th.AnyMessage())

	slog.Info("机器人已开始接收消息", "component", "console.bot", "account", account)
	color.Green("🔄 Bot is running... Press Ctrl+C to stop")

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

func configureBotMenu(ctx context.Context, bot *telego.Bot, console ports.Console) error {
	commands := console.Commands()
	menu := make([]telego.BotCommand, 0, len(commands))
	for _, command := range commands {
		menu = append(menu, telego.BotCommand{Command: command.Name, Description: command.Description})
	}
	return bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{Commands: menu})
}

func checkSessionAndMaybeStartWatch(ctx context.Context, watchCtrl watchControl, opts login.SessionOptions, autoStart bool) startupState {
	user, err := login.CheckSession(ctx, opts)
	switch {
	case err == nil:
		watchStatus := "监听模块已交给 Web 管理面板控制。"
		watchStarted := false
		if autoStart {
			watchStatus = "已进入监听，正在等待表情触发。"
			watchStarted = true
			if !watchCtrl.Start() {
				watchStatus = "已在运行。"
				watchStarted = watchCtrl.Running()
			}
		}
		return startupState{
			Session:      "有效。" + login.UserSummary(user),
			Watch:        watchStatus,
			WatchStarted: watchStarted,
		}
	case errors.Is(err, login.ErrSessionUnauthorized):
		hint := "请使用 /login_code 用户名 登录；登录成功后会自动重新启动 watch。"
		if !autoStart {
			hint = "请在 Web 管理面板完成登录，并在模块管理中启用监听下载。"
		}
		return startupState{
			Session: "无效或不存在。",
			Watch:   "未启动。",
			Hint:    hint,
		}
	default:
		hint := "请稍后重试，或使用 /login_code 用户名 重新登录；登录成功后会自动重新启动 watch。"
		if !autoStart {
			hint = "请稍后重试，或在 Web 管理面板重新登录。"
		}
		return startupState{
			Session: fmt.Sprintf("检查失败：%v", err),
			Watch:   "未启动。",
			Hint:    hint,
		}
	}
}

func notifyWatchAfterLogin(ctx context.Context, notifier *botNotifier, watchCtrl watchControl) {
	if watchCtrl.Start() {
		notifier.Notify(ctx, "登录完成，watch 已重新启动，正在监听表情触发。")
		return
	}
	notifier.Notify(ctx, "登录完成，watch 已在运行。")
}

func handleAllowedMessage(
	ctx *th.Context,
	msg *telego.Message,
	loginMgr ports.BotLogin,
	requestReboot func(),
	updateController *tdlUpdateController,
	watchCtrl watchControl,
	aria2Factory aria2ControllerFactory,
	localFactory localDownloadControllerFactory,
	maintenance ports.KVMaintenance,
	account types.AccountID,
	console ports.Console,
	extras ...ports.ConsoleContribution,
) error {
	fromID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if msg.Chat.Type != telego.ChatTypePrivate {
		if console.PrivateCommand(strings.TrimPrefix(commandName(text), "/")) {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
				tu.ID(chatID),
				"请在私聊中发送控制命令。",
			).WithReplyParameters(&telego.ReplyParameters{MessageID: msg.MessageID}))
		}
		return nil
	}

	if commandName(text) != "" && console != nil {
		response, handled, err := dispatchConsoleCommand(ctx, msg, console, account, commandAdapters(ctx, msg, loginMgr, requestReboot, updateController, aria2Factory, localFactory, maintenance, account), extras)
		if err != nil {
			return err
		}
		if handled {
			if response.Text != "" {
				return sendMessage(ctx, chatID, response.Text)
			}
			return nil
		}
	}
	// Reply keyboards and login input are transport conversations, not slash commands.
	if commandName(text) == "" {
		if handled, err := handleDownloadCommand(ctx, msg, text, aria2Factory, localFactory); handled || err != nil {
			return err
		}
	}
	if handled, err := handleMessageLinkSubmission(ctx, msg, text, watchCtrl); handled || err != nil {
		return err
	}

	if loginMgr.HandleInput(fromID, chatID, msg.Text, msg.MessageID) {
		return nil
	}

	slog.Debug("收到已授权用户的未处理消息", "component", "console.bot", "user_id", fromID)
	return nil
}

func commandName(text string) string {
	cmd, _, _ := tu.ParseCommand(text)
	if cmd == "" {
		return ""
	}
	return "/" + cmd
}

func loginNamespaceFromCommand(text string) (string, error) {
	_, _, payload := tu.ParseCommandPayload(text)
	fields := strings.Fields(payload)
	if len(fields) != 1 {
		return "", errors.New("login username is required")
	}
	return config.NormalizeNamespace(fields[0])
}

func sendLoginNamespaceUsage(ctx *th.Context, chatID int64, command string) {
	_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
		tu.ID(chatID),
		fmt.Sprintf("请在命令后填写用户名，例如：%s alice。\n用户名只能使用英文字母，用来区分保存在 .tdl 目录下的登录数据。", command),
	))
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

	switch strings.ToLower(u.Scheme) {
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
