package accounttelegram

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	webLoginTimeout = 10 * time.Minute

	loginStageSendingCode = "sending_code"
	loginStageCode        = "code"
	loginStagePassword    = "password"
	loginStageDone        = "done"
	loginStageFailed      = "failed"

	loginStatusSendingCode = "正在连接 Telegram 并发送验证码，请稍候；网络状况不佳时可能需要一些时间。"
	loginStatusCodeSent    = "验证码已发送，请输入 Telegram 收到的原始验证码。"
)

type Login struct {
	opts LoginOptions

	mu     sync.Mutex
	closed bool
	active *loginFlow
}

type loginFlow struct {
	mu           sync.Mutex
	kind         string
	stage        string
	status       string
	errText      string
	phone        string
	namespace    string
	user         map[string]any
	inputPending bool
	ctx          context.Context

	codeCh     chan string
	passwordCh chan string
	done       chan struct{}
	cancel     context.CancelFunc
}

// LoginOptions supplies transport and account selection at the composition boundary.
type LoginOptions struct {
	Context       context.Context
	Authenticate  func(context.Context, string, string, ports.LoginChallenge) (map[string]any, error)
	Prepare       func(string) (string, error)
	Complete      func(context.Context, string, map[string]any) (bool, error)
	RequestReboot func()
}

func NewLogin(opts LoginOptions) *Login {
	return &Login{opts: opts}
}

func (m *Login) StartPhone(parent context.Context, phone, namespace string) error {
	phone = normalizePhone(phone)
	if phone == "" {
		return errors.New("phone is required")
	}
	if m.opts.Authenticate == nil || m.opts.Prepare == nil {
		return errors.New("login transport is not configured")
	}
	namespace, err := m.opts.Prepare(namespace)
	if err != nil {
		return err
	}
	return m.start(parent, "phone", namespace, func(ctx context.Context, flow *loginFlow) (map[string]any, error) {
		return m.opts.Authenticate(ctx, namespace, phone, loginChallenge{flow: flow})
	}, func(flow *loginFlow) {
		flow.phone = phone
		flow.stage = loginStageSendingCode
		flow.status = loginStatusSendingCode
	})
}

func (m *Login) start(
	parent context.Context,
	kind string,
	namespace string,
	run func(context.Context, *loginFlow) (map[string]any, error),
	init func(*loginFlow),
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
	flow := &loginFlow{
		ctx:        ctx,
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
	if m.closed {
		m.mu.Unlock()
		cancel()
		return errors.New("login manager is stopped")
	}
	if m.active != nil && !m.active.finished() {
		m.mu.Unlock()
		cancel()
		return errors.New("another login flow is already active")
	}
	m.active = flow
	m.mu.Unlock()
	slog.Info("Telegram 登录已开始", "component", ID, "account", namespace, "login_method", kind)

	go func() {
		defer cancel()
		defer close(flow.done)
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("Telegram 登录异常中断", "component", ID, "account", namespace)
				flow.muSet(func() {
					flow.stage = loginStageFailed
					flow.status = "登录失败。"
					flow.errText = fmt.Sprintf("login adapter panic: %v", recovered)
				})
			}
		}()
		user, err := run(ctx, flow)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			level := slog.LevelError
			if errors.Is(err, context.Canceled) {
				level = slog.LevelDebug
			}
			slog.Log(ctx, level, "Telegram 登录未完成", "component", ID, "account", namespace, "error", err)
			flow.muSet(func() {
				flow.stage = loginStageFailed
				flow.status = "登录失败。"
				flow.errText = loginErrorText(err)
			})
			return
		}
		var restart bool
		if m.opts.Complete != nil {
			restart, err = m.opts.Complete(ctx, flow.namespace, user)
		}
		if err != nil {
			slog.Error("保存 Telegram 登录结果失败", "component", ID, "account", namespace, "error", err)
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
			flow.user = maps.Clone(user)
		})
		slog.Info("Telegram 登录已完成", "component", ID, "account", namespace, "restart", restart)
		if restart && m.opts.RequestReboot != nil {
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
				m.opts.RequestReboot()
			}
		}
	}()
	return nil
}

func (m *Login) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	flow := m.active
	m.mu.Unlock()
	if flow == nil {
		return nil
	}
	flow.cancel()
	select {
	case <-flow.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Login) Status() types.LoginStatus {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow == nil {
		return types.LoginStatus{Active: false, Status: "当前没有登录流程。"}
	}

	flow.mu.Lock()
	defer flow.mu.Unlock()
	return types.LoginStatus{
		Active:    !flow.isTerminalLocked(),
		Kind:      flow.kind,
		Stage:     flow.stage,
		Status:    flow.status,
		Error:     flow.errText,
		Phone:     flow.phone,
		Namespace: flow.namespace,
		User:      maps.Clone(flow.user),
	}
}

func (m *Login) SubmitCode(code string) error {
	flow, err := m.requireActive()
	if err != nil {
		return err
	}
	return flow.sendCode(strings.TrimSpace(code))
}

func (m *Login) SubmitPassword(password string) error {
	flow, err := m.requireActive()
	if err != nil {
		return err
	}
	return flow.sendPassword(password)
}

func (m *Login) Cancel() {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow != nil {
		flow.cancel()
	}
}

func (m *Login) requireActive() (*loginFlow, error) {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()
	if flow == nil || flow.finished() {
		return nil, errors.New("no active login flow")
	}
	return flow, nil
}

type loginChallenge struct{ flow *loginFlow }

func (a loginChallenge) Code(ctx context.Context) (string, error) {
	a.flow.prompt(loginStageCode, loginStatusCodeSent)
	return a.flow.waitCode(ctx)
}

func (a loginChallenge) Password(ctx context.Context) (string, error) {
	a.flow.prompt(loginStagePassword, "请输入 Telegram 2FA 密码。")
	return a.flow.waitPassword(ctx)
}

func (a loginChallenge) AuthInputError(ctx context.Context, input string, err error) error {
	return a.flow.authInputError(ctx, input, err)
}

func (f *loginFlow) prompt(stage, status string) {
	f.muSet(func() {
		f.stage = stage
		f.inputPending = false
		if f.errText == "" {
			f.status = status
		}
	})
}

func (f *loginFlow) authInputError(_ context.Context, input string, err error) error {
	stage := input
	if stage == "" {
		stage = loginStagePassword
	}
	message := loginInputErrorText(input, err)
	f.muSet(func() {
		f.stage = stage
		f.status = message
		f.errText = message
	})
	return nil
}

func (f *loginFlow) verifying(stage, status string) {
	f.muSet(func() {
		f.stage = stage
		f.status = status
		f.errText = ""
	})
}

func (f *loginFlow) muSet(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *loginFlow) waitCode(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-f.codeCh:
		if code == "" {
			return "", errors.New("code is empty")
		}
		f.verifying(loginStageCode, "正在验证验证码...")
		return code, nil
	}
}

func (f *loginFlow) waitPassword(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case password := <-f.passwordCh:
		if password == "" {
			return "", errors.New("password is empty")
		}
		f.verifying(loginStagePassword, "正在验证 2FA 密码...")
		return password, nil
	}
}

func (f *loginFlow) sendCode(code string) error {
	if code == "" {
		return errors.New("code is empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx != nil && f.ctx.Err() != nil {
		return f.ctx.Err()
	}
	if f.stage != loginStageCode {
		return errors.New("验证码尚未发送，请稍候。")
	}
	if f.inputPending {
		return errors.New("code has already been submitted")
	}
	select {
	case f.codeCh <- code:
		f.inputPending = true
		f.stage = loginStageCode
		f.status = "正在验证验证码..."
		f.errText = ""
		return nil
	default:
		return errors.New("code has already been submitted")
	}
}

func (f *loginFlow) sendPassword(password string) error {
	if password == "" {
		return errors.New("password is empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx != nil && f.ctx.Err() != nil {
		return f.ctx.Err()
	}
	if f.stage != loginStagePassword {
		return errors.New("password has not been requested")
	}
	if f.inputPending {
		return errors.New("password has already been submitted")
	}
	select {
	case f.passwordCh <- password:
		f.inputPending = true
		f.status = "正在验证 2FA 密码..."
		f.errText = ""
		return nil
	default:
		return errors.New("password has already been submitted")
	}
}

func (f *loginFlow) finished() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *loginFlow) isTerminalLocked() bool {
	return f.stage == loginStageDone || f.stage == loginStageFailed
}

func normalizePhone(phone string) string {
	return strings.NewReplacer(" ", "", "\t", "", "-", "", "(", "", ")", "").Replace(phone)
}

func loginErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "retryuntilack") && strings.Contains(lower, "retry limit reached") {
		return "连接 Telegram 超时，未能完成初始化。请检查服务器能否访问 Telegram；如果需要代理，请在配置中填写 proxy，也可以适当调大 reconnect_timeout 后重试。原始错误：" + text
	}
	return text
}

func loginInputErrorText(input string, err error) string {
	switch input {
	case loginStageCode:
		return "验证码不正确，请重新输入。"
	case loginStagePassword:
		return "2FA 密码不正确，请重新输入。"
	default:
		return loginErrorText(err)
	}
}
