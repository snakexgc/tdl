package bot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/core/util/netutil"
)

type Options struct {
	Token        string
	AllowedUsers []int64
	Proxy        string
}

func Run(ctx context.Context, opts Options) (rerr error) {
	if opts.Token == "" {
		return errors.New("bot token is empty, please set bot.token in config.json")
	}

	// create telego bot with proxy
	bot, err := newBot(opts.Token, opts.Proxy)
	if err != nil {
		return errors.Wrap(err, "create bot")
	}

	// verify bot identity
	botUser, err := bot.GetMe(ctx)
	if err != nil {
		return errors.Wrap(err, "get bot info")
	}
	color.Green("🤖 Bot @%s (ID: %d) started", botUser.Username, botUser.ID)

	// build allowed users set
	allowedSet := make(map[int64]bool, len(opts.AllowedUsers))
	for _, uid := range opts.AllowedUsers {
		allowedSet[uid] = true
	}

	// send startup notification to allowed users
	for _, uid := range opts.AllowedUsers {
		_, err := bot.SendMessage(ctx, tu.Message(
			tu.ID(uid),
			fmt.Sprintf("🤖 Bot @%s 已启动", botUser.Username),
		))
		if err != nil {
			color.Yellow("⚠️ Failed to notify user %d: %v", uid, err)
		} else {
			color.Green("📩 Notified user %d", uid)
		}
	}

	// start long polling
	updates, err := bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 10,
	})
	if err != nil {
		return errors.Wrap(err, "start long polling")
	}

	// create bot handler
	bh, err := th.NewBotHandler(bot, updates)
	if err != nil {
		return errors.Wrap(err, "create bot handler")
	}

	// handle all text messages from unauthorized users
	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.Message == nil || update.Message.From == nil {
			return nil
		}

		fromID := update.Message.From.ID
		chatID := update.Message.Chat.ID

		// check if user is allowed
		if allowedSet[fromID] {
			color.Cyan("✅ Allowed user %d sent message", fromID)
			return nil
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

	bh.Start()

	return nil
}

// newBot creates a telego Bot instance with optional proxy support.
func newBot(token, proxyURL string) (*telego.Bot, error) {
	opts := []telego.BotOption{
		telego.WithDefaultLogger(false, true),
	}

	if proxyURL != "" {
		httpClient, err := newHTTPClientWithProxy(proxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "create http client with proxy")
		}
		opts = append(opts, telego.WithHTTPClient(httpClient))
	}

	bot, err := telego.NewBot(token, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "create telego bot")
	}

	return bot, nil
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
