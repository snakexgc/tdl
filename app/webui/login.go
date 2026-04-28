package webui

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/skip2/go-qrcode"

	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

const (
	webLoginTimeout = 10 * time.Minute

	loginStageDone   = "done"
	loginStageFailed = "failed"
)

type webLoginManager struct {
	opts Options

	mu     sync.Mutex
	active *webLoginFlow
}

type webLoginFlow struct {
	mu        sync.Mutex
	kind      string
	stage     string
	status    string
	errText   string
	phone     string
	namespace string
	qrURL     string
	qrImage   string
	user      map[string]any

	codeCh     chan string
	passwordCh chan string
	done       chan struct{}
	cancel     context.CancelFunc
}

type webLoginStatus struct {
	Active    bool           `json:"active"`
	Kind      string         `json:"kind,omitempty"`
	Stage     string         `json:"stage,omitempty"`
	Status    string         `json:"status,omitempty"`
	Error     string         `json:"error,omitempty"`
	Phone     string         `json:"phone,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	QRURL     string         `json:"qr_url,omitempty"`
	QRImage   string         `json:"qr_image,omitempty"`
	User      map[string]any `json:"user,omitempty"`
}

func newWebLoginManager(opts Options) *webLoginManager {
	return &webLoginManager{opts: opts}
}

func (m *webLoginManager) startPhone(parent context.Context, phone, namespace string) error {
	phone = normalizePhone(phone)
	if phone == "" {
		return errors.New("phone is required")
	}
	namespace, kvd, err := m.openNamespaceKV(namespace)
	if err != nil {
		return err
	}
	return m.start(parent, "phone", namespace, func(ctx context.Context, flow *webLoginFlow) (*tg.User, error) {
		flow.set("code", "验证码已发送，请直接输入 Telegram 收到的原始验证码。")
		authenticator := webCodeAuthenticator{
			flow:  flow,
			phone: phone,
		}
		return login.CodeWithAuthenticator(ctx, m.sessionOptions(kvd), authenticator)
	}, func(flow *webLoginFlow) {
		flow.phone = phone
	})
}

func (m *webLoginManager) startQR(parent context.Context, namespace string) error {
	namespace, kvd, err := m.openNamespaceKV(namespace)
	if err != nil {
		return err
	}
	return m.start(parent, "qr", namespace, func(ctx context.Context, flow *webLoginFlow) (*tg.User, error) {
		show := func(ctx context.Context, token qrlogin.Token) error {
			png, err := qrcode.Encode(token.URL(), qrcode.Medium, 512)
			if err != nil {
				return errors.Wrap(err, "create QR image")
			}
			flow.muSet(func() {
				flow.stage = "qr"
				flow.status = "请使用 Telegram 客户端扫描二维码。"
				flow.qrURL = token.URL()
				flow.qrImage = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			})
			return nil
		}
		password := func(ctx context.Context) (string, error) {
			flow.set("password", "请输入 Telegram 2FA 密码。")
			return flow.waitPassword(ctx)
		}
		return login.QRWithCallbacks(ctx, m.sessionOptions(kvd), show, password)
	}, nil)
}

func (m *webLoginManager) start(
	parent context.Context,
	kind string,
	namespace string,
	run func(context.Context, *webLoginFlow) (*tg.User, error),
	init func(*webLoginFlow),
) error {
	if parent != nil {
		select {
		case <-parent.Done():
			return parent.Err()
		default:
		}
	}
	base := m.opts.Context
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, webLoginTimeout)
	flow := &webLoginFlow{
		kind:       kind,
		stage:      "starting",
		status:     "登录流程已开始。",
		namespace:  namespace,
		codeCh:     make(chan string, 1),
		passwordCh: make(chan string, 1),
		done:       make(chan struct{}),
		cancel:     cancel,
	}
	if init != nil {
		init(flow)
	}

	m.mu.Lock()
	if m.active != nil && !m.active.finished() {
		m.mu.Unlock()
		cancel()
		return errors.New("another login flow is already active")
	}
	m.active = flow
	m.mu.Unlock()

	go func() {
		defer cancel()
		defer close(flow.done)
		user, err := run(ctx, flow)
		if err != nil {
			flow.muSet(func() {
				flow.stage = loginStageFailed
				flow.status = "登录失败。"
				flow.errText = err.Error()
			})
			return
		}
		restart, err := m.saveNamespaceIfChanged(flow.namespace)
		if err != nil {
			flow.muSet(func() {
				flow.stage = loginStageFailed
				flow.status = "登录已完成，但保存用户配置失败。"
				flow.errText = err.Error()
			})
			return
		}
		flow.muSet(func() {
			flow.stage = loginStageDone
			flow.status = "登录成功。"
			if restart {
				flow.status = "登录成功，正在重启以切换到该用户。"
			}
			flow.user = telegramUserInfo(user)
		})
		if restart && m.opts.RequestReboot != nil {
			go func() {
				time.Sleep(300 * time.Millisecond)
				m.opts.RequestReboot()
			}()
			return
		}
		if m.opts.OnLoginSuccess != nil {
			m.opts.OnLoginSuccess(user)
		}
	}()
	return nil
}

func (m *webLoginManager) openNamespaceKV(raw string) (string, storage.Storage, error) {
	namespace, err := config.NormalizeNamespace(raw)
	if err != nil {
		return "", nil, err
	}
	if m.opts.KVEngine != nil {
		kvd, err := m.opts.KVEngine.Open(namespace)
		if err != nil {
			return "", nil, errors.Wrap(err, "open namespace storage")
		}
		return namespace, kvd, nil
	}
	if namespace == m.currentNamespace() && m.opts.NamespaceKV != nil {
		return namespace, m.opts.NamespaceKV, nil
	}
	return "", nil, errors.New("namespace storage is not configured")
}

func (m *webLoginManager) saveNamespaceIfChanged(namespace string) (bool, error) {
	cfg := config.Get()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if cfg.Namespace == namespace {
		return false, nil
	}
	next, err := cloneConfig(cfg)
	if err != nil {
		return false, err
	}
	next.Namespace = namespace
	if err := config.Set(next); err != nil {
		return false, err
	}
	return true, nil
}

func (m *webLoginManager) currentNamespace() string {
	if m.opts.Namespace != "" {
		return m.opts.Namespace
	}
	cfg := config.Get()
	if cfg != nil {
		return cfg.Namespace
	}
	return ""
}

func (m *webLoginManager) sessionOptions(kvd storage.Storage) login.SessionOptions {
	cfg := config.Get()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return login.SessionOptions{
		KV:               kvd,
		Proxy:            cfg.Proxy,
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	}
}

func (m *webLoginManager) status() webLoginStatus {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow == nil {
		return webLoginStatus{Active: false, Status: "当前没有登录流程。"}
	}

	flow.mu.Lock()
	defer flow.mu.Unlock()
	return webLoginStatus{
		Active:    !flow.isTerminalLocked(),
		Kind:      flow.kind,
		Stage:     flow.stage,
		Status:    flow.status,
		Error:     flow.errText,
		Phone:     flow.phone,
		Namespace: flow.namespace,
		QRURL:     flow.qrURL,
		QRImage:   flow.qrImage,
		User:      flow.user,
	}
}

func (m *webLoginManager) submitCode(code string) error {
	flow, err := m.requireActive()
	if err != nil {
		return err
	}
	return flow.sendCode(strings.TrimSpace(code))
}

func (m *webLoginManager) submitPassword(password string) error {
	flow, err := m.requireActive()
	if err != nil {
		return err
	}
	return flow.sendPassword(strings.TrimSpace(password))
}

func (m *webLoginManager) cancel() {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow != nil {
		flow.cancel()
	}
}

func (m *webLoginManager) requireActive() (*webLoginFlow, error) {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow == nil || flow.finished() {
		return nil, errors.New("no active login flow")
	}
	return flow, nil
}

type webCodeAuthenticator struct {
	flow  *webLoginFlow
	phone string
}

func (a webCodeAuthenticator) Phone(ctx context.Context) (string, error) {
	return a.phone, nil
}

func (a webCodeAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	a.flow.set("code", "验证码已发送，请直接输入 Telegram 收到的原始验证码。")
	return a.flow.waitCode(ctx)
}

func (a webCodeAuthenticator) Password(ctx context.Context) (string, error) {
	a.flow.set("password", "请输入 Telegram 2FA 密码。")
	return a.flow.waitPassword(ctx)
}

func (a webCodeAuthenticator) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign up is not supported")
}

func (a webCodeAuthenticator) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

func (f *webLoginFlow) set(stage, status string) {
	f.muSet(func() {
		f.stage = stage
		f.status = status
	})
}

func (f *webLoginFlow) muSet(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *webLoginFlow) waitCode(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-f.codeCh:
		if code == "" {
			return "", errors.New("code is empty")
		}
		return code, nil
	}
}

func (f *webLoginFlow) waitPassword(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case password := <-f.passwordCh:
		if password == "" {
			return "", errors.New("password is empty")
		}
		return password, nil
	}
}

func (f *webLoginFlow) sendCode(code string) error {
	if code == "" {
		return errors.New("code is empty")
	}
	select {
	case f.codeCh <- code:
		return nil
	default:
		return errors.New("code has already been submitted")
	}
}

func (f *webLoginFlow) sendPassword(password string) error {
	if password == "" {
		return errors.New("password is empty")
	}
	select {
	case f.passwordCh <- password:
		return nil
	default:
		return errors.New("password has already been submitted")
	}
}

func (f *webLoginFlow) finished() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *webLoginFlow) isTerminalLocked() bool {
	return f.stage == loginStageDone || f.stage == loginStageFailed
}

func normalizePhone(phone string) string {
	return strings.NewReplacer(" ", "", "\t", "", "-", "", "(", "", ")", "").Replace(phone)
}
