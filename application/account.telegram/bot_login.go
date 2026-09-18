package accounttelegram

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
)

const botPhoneStage = "phone"

const (
	defaultLoginInputTimeout = 5 * time.Minute
	defaultLoginFlowTimeout  = 10 * time.Minute
)

var (
	errLoginBusy         = ports.ErrLoginBusy
	errLoginInputTimeout = stderrors.New("login input timeout")
	errLoginInvalidUser  = stderrors.New("login finished without valid user")
)

type loginRunnerFactory func(string) (ports.LoginRunner, error)

type BotLogin struct {
	ctx           context.Context
	cancel        context.CancelFunc
	bot           ports.LoginMessenger
	runnerFactory loginRunnerFactory
	inputTimeout  time.Duration
	flowTimeout   time.Duration

	mu     sync.Mutex
	active *botLoginFlow
	closed bool

	onSuccess func(user *ports.LoginUser, namespace string)
}

type botLoginFlow struct {
	kind      string
	namespace string
	userID    int64
	chatID    int64

	input  chan loginInput
	cancel context.CancelFunc
	done   chan struct{}
	stage  string
}

type loginInput struct {
	text      string
	messageID int
}

func NewBotLogin(ctx context.Context, bot ports.LoginMessenger, factory loginRunnerFactory) *BotLogin {
	if ctx == nil {
		ctx = context.Background()
	}
	if factory == nil {
		factory = func(string) (ports.LoginRunner, error) {
			return nil, errors.New("login runner factory is not configured")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &BotLogin{
		ctx:           ctx,
		cancel:        cancel,
		bot:           bot,
		runnerFactory: factory,
		inputTimeout:  defaultLoginInputTimeout,
		flowTimeout:   defaultLoginFlowTimeout,
	}
}

func (m *BotLogin) StartCode(userID, chatID int64, namespace ...string) error {
	return m.start(loginStageCode, firstNamespace(namespace), userID, chatID, func(ctx context.Context, flow *botLoginFlow, runner ports.LoginRunner) (*ports.LoginUser, error) {
		return runner.LoginCode(ctx, botCodeAuthenticator{manager: m, flow: flow})
	})
}

func (m *BotLogin) Cancel(userID, chatID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil || !m.active.matches(userID, chatID) {
		return false
	}
	m.active.cancel()
	return true
}

func (m *BotLogin) HandleInput(userID, chatID int64, text string, messageID int) bool {
	m.mu.Lock()
	flow := m.active
	m.mu.Unlock()

	if flow == nil || !flow.matches(userID, chatID) {
		return false
	}

	select {
	case flow.input <- loginInput{text: text, messageID: messageID}:
		return true
	case <-flow.done:
		return false
	case <-m.ctx.Done():
		return false
	default:
		return false
	}
}

func (m *BotLogin) ActiveFor(userID, chatID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.active != nil && m.active.matches(userID, chatID)
}

func (m *BotLogin) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active != nil
}

func (m *BotLogin) SetOnSuccess(fn func(user *ports.LoginUser, namespace string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSuccess = fn
}

func (m *BotLogin) activeStage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ""
	}
	return m.active.stage
}

func (m *BotLogin) start(
	kind string,
	namespace string,
	userID int64,
	chatID int64,
	run func(ctx context.Context, flow *botLoginFlow, runner ports.LoginRunner) (*ports.LoginUser, error),
) error {
	runner, err := m.runnerFactory(namespace)
	if err != nil {
		return err
	}
	if runner == nil {
		return errors.New("login runner is not configured")
	}

	ctx, cancel := context.WithTimeout(m.ctx, m.flowTimeout)
	flow := &botLoginFlow{
		kind:      kind,
		namespace: namespace,
		userID:    userID,
		chatID:    chatID,
		input:     make(chan loginInput, 8),
		cancel:    cancel,
		done:      make(chan struct{}),
		stage:     "starting",
	}

	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil || m.active != nil {
		m.mu.Unlock()
		cancel()
		return errLoginBusy
	}
	m.active = flow
	m.mu.Unlock()

	go func() {
		defer cancel()
		defer m.finish(flow)

		startMessage := "登录流程已开始，发送 /cancel_login 可以取消。"
		if flow.namespace != "" {
			startMessage = fmt.Sprintf("用户 %s 的登录流程已开始，发送 /cancel_login 可以取消。", flow.namespace)
		}
		if err := m.sendText(chatID, startMessage); err != nil {
			return
		}

		user, err := run(ctx, flow, runner)
		if err != nil {
			m.notifyFailure(flow, err)
			return
		}
		if err := ctx.Err(); err != nil {
			m.notifyFailure(flow, err)
			return
		}
		if !validLoginUser(user) {
			m.notifyFailure(flow, errLoginInvalidUser)
			return
		}

		_ = m.sendText(chatID, "登录成功！\n"+loginUserSummary(user))
		if onSuccess := m.successHandler(); onSuccess != nil {
			onSuccess(user, flow.namespace)
		}
	}()

	return nil
}

func (m *BotLogin) finish(flow *botLoginFlow) {
	m.mu.Lock()
	if m.active == flow {
		m.active = nil
	}
	m.mu.Unlock()

	close(flow.done)
}

func (m *BotLogin) setStage(flow *botLoginFlow, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == flow {
		flow.stage = stage
	}
}

func (m *BotLogin) successHandler() func(user *ports.LoginUser, namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onSuccess
}

func (m *BotLogin) ask(ctx context.Context, flow *botLoginFlow, stage, prompt string, sensitive bool) (string, error) {
	for {
		m.setStage(flow, stage)
		if err := m.sendText(flow.chatID, prompt); err != nil {
			return "", err
		}

		timer := time.NewTimer(m.inputTimeout)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return "", ctx.Err()
		case <-m.ctx.Done():
			stopTimer(timer)
			return "", m.ctx.Err()
		case <-timer.C:
			return "", errLoginInputTimeout
		case input := <-flow.input:
			stopTimer(timer)
			if sensitive {
				m.deleteMessage(flow.chatID, input.messageID)
			}
			value := input.text
			if stage != loginStagePassword {
				value = strings.TrimSpace(value)
			}
			if value == "" {
				if err := m.sendText(flow.chatID, "输入不能为空，请重新发送。"); err != nil {
					return "", err
				}
				continue
			}
			return value, nil
		}
	}
}

// Stop closes admission and waits for authentication and completion callbacks
// to release the account resources. A timed-out stop may be retried.
func (m *BotLogin) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	flow := m.active
	if flow != nil {
		flow.cancel()
	}
	m.mu.Unlock()
	if flow == nil {
		return nil
	}
	select {
	case <-flow.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *BotLogin) notifyFailure(flow *botLoginFlow, err error) {
	switch {
	case stderrors.Is(err, context.Canceled):
		_ = m.sendText(flow.chatID, "登录已取消。")
	case stderrors.Is(err, context.DeadlineExceeded), stderrors.Is(err, errLoginInputTimeout):
		_ = m.sendText(flow.chatID, "登录已超时，请重新发送 /login_code 用户名。")
	case stderrors.Is(err, errLoginInvalidUser):
		_ = m.sendText(flow.chatID, "登录失败：未获取到有效账号，请重新发送 /login_code 用户名。")
	default:
		_ = m.sendText(flow.chatID, fmt.Sprintf("登录失败：%v", err))
	}
}

func (m *BotLogin) reportInputError(_ context.Context, flow *botLoginFlow, input string, _ error) error {
	switch input {
	case loginStageCode:
		return m.sendText(flow.chatID, "验证码不正确，请重新发送。")
	case loginStagePassword:
		return m.sendText(flow.chatID, "2FA 密码不正确，请重新发送。")
	default:
		return nil
	}
}

func (m *BotLogin) sendText(chatID int64, text string) error {
	return m.bot.SendText(m.ctx, chatID, text)
}

func (m *BotLogin) deleteMessage(chatID int64, messageID int) {
	if messageID > 0 {
		_ = m.bot.DeleteInput(m.ctx, chatID, messageID)
	}
}

func (f *botLoginFlow) matches(userID, chatID int64) bool {
	return f.userID == userID && f.chatID == chatID
}

func validLoginUser(user *ports.LoginUser) bool {
	return user != nil && user.ID != 0
}

func firstNamespace(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

type botCodeAuthenticator struct {
	manager *BotLogin
	flow    *botLoginFlow
}

func (a botCodeAuthenticator) Phone(ctx context.Context) (string, error) {
	phone, err := a.manager.ask(ctx, a.flow, botPhoneStage, "请输入 Telegram 手机号（包含国家区号，例如 +8613800000000）：", false)
	if err != nil {
		return "", err
	}
	return normalizePhone(phone), nil
}

func (a botCodeAuthenticator) Code(ctx context.Context) (string, error) {
	for {
		masked, err := a.manager.ask(ctx, a.flow, loginStageCode, "验证码已发送，请将收到的登录验证码每位数字 +1 后发送（9 变 0）。例如收到 56789，请发送 67890：", true)
		if err != nil {
			return "", err
		}

		code, ok := unmaskLoginCode(masked)
		if ok {
			return code, nil
		}

		if err := a.manager.sendText(a.flow.chatID, "验证码只能包含数字，请重新发送。"); err != nil {
			return "", err
		}
	}
}

func (a botCodeAuthenticator) Password(ctx context.Context) (string, error) {
	return a.manager.ask(ctx, a.flow, loginStagePassword, "请输入 2FA 密码：", true)
}

func (a botCodeAuthenticator) AuthInputError(ctx context.Context, input string, err error) error {
	return a.manager.reportInputError(ctx, a.flow, input, err)
}

func unmaskLoginCode(masked string) (string, bool) {
	if masked == "" {
		return "", false
	}

	var builder strings.Builder
	builder.Grow(len(masked))
	for _, r := range masked {
		if r < '0' || r > '9' {
			return "", false
		}
		if r == '0' {
			builder.WriteByte('9')
			continue
		}
		builder.WriteRune(r - 1)
	}

	return builder.String(), true
}

func loginUserSummary(user *ports.LoginUser) string {
	if user == nil || user.ID == 0 {
		return "ID: (invalid), Username: (not set), Name: (not set)"
	}

	username := user.Username
	if username == "" {
		username = "(not set)"
	}
	name := user.FirstName
	if user.LastName != "" {
		if name != "" {
			name += " "
		}
		name += user.LastName
	}
	if name == "" {
		name = "(not set)"
	}

	return "ID: " + strconv.FormatInt(user.ID, 10) + ", Username: " + username + ", Name: " + name
}
