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
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/util/netutil"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
)

type Options struct {
	Token            string
	AllowedUsers     []int64
	Proxy            string
	Namespace        string
	NTP              string
	ReconnectTimeout time.Duration
	Watch            watch.Options
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

	// build allowed users set
	allowedSet := make(map[int64]bool, len(opts.AllowedUsers))
	for _, uid := range opts.AllowedUsers {
		allowedSet[uid] = true
	}

	notifier.Notify(ctx, startupMessage(botUser))

	if err := configureBotMenu(ctx, bot); err != nil {
		return errors.Wrap(err, "create bot menu")
	}

	kvd, err := kv.From(ctx).Open(opts.Namespace)
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
	loginMgr.SetOnSuccess(func(user *tg.User) {
		notifier.Notify(ctx, "MTProto session 已更新。\n"+login.UserSummary(user))
		startWatch(ctx, notifier, watchCtrl)
	})

	checkSessionAndMaybeStartWatch(ctx, notifier, watchCtrl, sessionOpts)

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

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.Message == nil || update.Message.From == nil {
			return nil
		}

		fromID := update.Message.From.ID
		chatID := update.Message.Chat.ID

		// check if user is allowed
		if allowedSet[fromID] {
			return handleAllowedMessage(ctx, update.Message, loginMgr)
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
		botLogger.SetShuttingDown()
		watchCtrl.Stop()

		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bh.StopWithContext(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
			color.Yellow("⚠️ Stop bot handler: %v", err)
		}
		cancelPolling()
	}()

	err = bh.Start()
	botLogger.SetShuttingDown()
	cancelPolling()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func startupMessage(botUser *telego.User) string {
	botName := "(unknown)"
	if botUser != nil && botUser.Username != "" {
		botName = "@" + botUser.Username
	} else if botUser != nil {
		botName = fmt.Sprintf("Bot %d", botUser.ID)
	}

	return fmt.Sprintf("%s 已启动。\n\n%s", botName, versionSummary())
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
			{Command: "login_code", Description: "使用验证码登录 MTProto"},
			{Command: "login_qr", Description: "使用二维码登录 MTProto"},
			{Command: "cancel_login", Description: "取消正在进行的登录"},
		},
	})
}

func checkSessionAndMaybeStartWatch(ctx context.Context, notifier *botNotifier, watchCtrl *watchController, opts login.SessionOptions) {
	user, err := login.CheckSession(ctx, opts)
	switch {
	case err == nil:
		notifier.Notify(ctx, "MTProto session 有效。\n"+login.UserSummary(user))
		startWatch(ctx, notifier, watchCtrl)
	case errors.Is(err, login.ErrSessionUnauthorized):
		notifier.Notify(ctx, "MTProto session 已失效或不存在，请使用 /login_code 或 /login_qr 重新登录。")
	default:
		notifier.Notify(ctx, fmt.Sprintf("检查 MTProto session 失败：%v\n请稍后重试，或使用 /login_code 或 /login_qr 重新登录。", err))
	}
}

func startWatch(ctx context.Context, notifier *botNotifier, watchCtrl *watchController) {
	if watchCtrl.Start() {
		notifier.Notify(ctx, "watch 流程已启动，正在监听表情触发。")
		return
	}
	notifier.Notify(ctx, "watch 流程已在运行。")
}

func handleAllowedMessage(ctx *th.Context, msg *telego.Message, loginMgr *loginManager) error {
	fromID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if msg.Chat.Type != telego.ChatTypePrivate {
		if isLoginCommand(text) {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
				tu.ID(chatID),
				"请在私聊中发送登录命令。",
			).WithReplyParameters(&telego.ReplyParameters{MessageID: msg.MessageID}))
		}
		return nil
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

func isLoginCommand(text string) bool {
	switch commandName(text) {
	case "/login_code", "/login_qr", "/cancel_login":
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
