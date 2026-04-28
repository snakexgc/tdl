package bot

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"

	"github.com/iyear/tdl/app/login"
)

const (
	defaultLoginInputTimeout = 5 * time.Minute
	defaultLoginFlowTimeout  = 10 * time.Minute
)

var (
	errLoginBusy         = stderrors.New("login flow already active")
	errLoginInputTimeout = stderrors.New("login input timeout")
	errLoginInvalidUser  = stderrors.New("login finished without valid user")
)

type botAPI interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
	SendPhoto(ctx context.Context, params *telego.SendPhotoParams) (*telego.Message, error)
	DeleteMessage(ctx context.Context, params *telego.DeleteMessageParams) error
}

type loginRunner interface {
	LoginCode(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error)
	LoginQR(ctx context.Context, show login.QRShowFunc, password login.PasswordFunc) (*tg.User, error)
}

type loginRunnerFactory func(namespace string) (loginRunner, error)

type gotdLoginRunner struct {
	opts login.SessionOptions
}

func (r gotdLoginRunner) LoginCode(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
	return login.CodeWithAuthenticator(ctx, r.opts, authenticator)
}

func (r gotdLoginRunner) LoginQR(ctx context.Context, show login.QRShowFunc, password login.PasswordFunc) (*tg.User, error) {
	return login.QRWithCallbacks(ctx, r.opts, show, password)
}

type loginManager struct {
	ctx           context.Context
	bot           botAPI
	runnerFactory loginRunnerFactory
	inputTimeout  time.Duration
	flowTimeout   time.Duration

	mu     sync.Mutex
	active *loginFlow

	onSuccess func(user *tg.User, namespace string)
}

type loginFlow struct {
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

func newLoginManager(ctx context.Context, bot botAPI, runner loginRunner) *loginManager {
	return newLoginManagerWithFactory(ctx, bot, func(string) (loginRunner, error) {
		return runner, nil
	})
}

func newLoginManagerWithFactory(ctx context.Context, bot botAPI, factory loginRunnerFactory) *loginManager {
	if ctx == nil {
		ctx = context.Background()
	}
	if factory == nil {
		factory = func(string) (loginRunner, error) {
			return nil, errors.New("login runner factory is not configured")
		}
	}
	return &loginManager{
		ctx:           ctx,
		bot:           bot,
		runnerFactory: factory,
		inputTimeout:  defaultLoginInputTimeout,
		flowTimeout:   defaultLoginFlowTimeout,
	}
}

func (m *loginManager) StartCode(userID, chatID int64, namespace ...string) error {
	return m.start("code", firstNamespace(namespace), userID, chatID, func(ctx context.Context, flow *loginFlow, runner loginRunner) (*tg.User, error) {
		return runner.LoginCode(ctx, botCodeAuthenticator{manager: m, flow: flow})
	})
}

func (m *loginManager) StartQR(userID, chatID int64, namespace ...string) error {
	return m.start("qr", firstNamespace(namespace), userID, chatID, func(ctx context.Context, flow *loginFlow, runner loginRunner) (*tg.User, error) {
		show := func(ctx context.Context, token qrlogin.Token) error {
			return m.showQR(ctx, flow, token)
		}
		password := func(ctx context.Context) (string, error) {
			return m.ask(ctx, flow, "password", "请输入 Telegram 2FA 密码：", true)
		}
		return runner.LoginQR(ctx, show, password)
	})
}

func (m *loginManager) Cancel(userID, chatID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil || !m.active.matches(userID, chatID) {
		return false
	}
	m.active.cancel()
	return true
}

func (m *loginManager) HandleInput(userID, chatID int64, text string, messageID int) bool {
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

func (m *loginManager) ActiveFor(userID, chatID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.active != nil && m.active.matches(userID, chatID)
}

func (m *loginManager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active != nil
}

func (m *loginManager) SetOnSuccess(fn func(user *tg.User, namespace string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSuccess = fn
}

func (m *loginManager) activeStage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ""
	}
	return m.active.stage
}

func (m *loginManager) start(
	kind string,
	namespace string,
	userID int64,
	chatID int64,
	run func(ctx context.Context, flow *loginFlow, runner loginRunner) (*tg.User, error),
) error {
	runner, err := m.runnerFactory(namespace)
	if err != nil {
		return err
	}
	if runner == nil {
		return errors.New("login runner is not configured")
	}

	ctx, cancel := context.WithTimeout(m.ctx, m.flowTimeout)
	flow := &loginFlow{
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
	if m.active != nil {
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

		_ = m.sendText(chatID, "登录成功！\n"+login.UserSummary(user))
		if onSuccess := m.successHandler(); onSuccess != nil {
			onSuccess(user, flow.namespace)
		}
	}()

	return nil
}

func (m *loginManager) finish(flow *loginFlow) {
	m.mu.Lock()
	if m.active == flow {
		m.active = nil
	}
	m.mu.Unlock()

	close(flow.done)
}

func (m *loginManager) setStage(flow *loginFlow, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == flow {
		flow.stage = stage
	}
}

func (m *loginManager) successHandler() func(user *tg.User, namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onSuccess
}

func (m *loginManager) ask(ctx context.Context, flow *loginFlow, stage, prompt string, sensitive bool) (string, error) {
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
			value := strings.TrimSpace(input.text)
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

func (m *loginManager) showQR(_ context.Context, flow *loginFlow, token qrlogin.Token) error {
	m.setStage(flow, "qr")

	png, err := qrcode.Encode(token.URL(), qrcode.Medium, 512)
	if err != nil {
		return errors.Wrap(err, "create qr image")
	}

	caption := "请用 Telegram 客户端扫描二维码完成登录。\n如果图片无法识别，也可以打开：\n" + token.URL()
	_, err = m.bot.SendPhoto(m.ctx, tu.Photo(
		tu.ID(flow.chatID),
		tu.FileFromBytes(png, "tdl-login-qr.png"),
	).WithCaption(caption))
	return err
}

func (m *loginManager) notifyFailure(flow *loginFlow, err error) {
	switch {
	case stderrors.Is(err, context.Canceled):
		_ = m.sendText(flow.chatID, "登录已取消。")
	case stderrors.Is(err, context.DeadlineExceeded), stderrors.Is(err, errLoginInputTimeout):
		_ = m.sendText(flow.chatID, "登录已超时，请重新发送 /login_code 用户名 或 /login_qr 用户名。")
	case stderrors.Is(err, errLoginInvalidUser):
		_ = m.sendText(flow.chatID, "登录失败：未获取到有效账号，请重新发送 /login_code 用户名 或 /login_qr 用户名。")
	default:
		_ = m.sendText(flow.chatID, fmt.Sprintf("登录失败：%v", err))
	}
}

func (m *loginManager) sendText(chatID int64, text string) error {
	_, err := m.bot.SendMessage(m.ctx, tu.Message(tu.ID(chatID), text))
	return err
}

func (m *loginManager) deleteMessage(chatID int64, messageID int) {
	if messageID <= 0 {
		return
	}
	_ = m.bot.DeleteMessage(m.ctx, &telego.DeleteMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
	})
}

func (f *loginFlow) matches(userID, chatID int64) bool {
	return f.userID == userID && f.chatID == chatID
}

func validLoginUser(user *tg.User) bool {
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
	manager *loginManager
	flow    *loginFlow
}

func (a botCodeAuthenticator) Phone(ctx context.Context) (string, error) {
	phone, err := a.manager.ask(ctx, a.flow, "phone", "请输入 Telegram 手机号（包含国家区号，例如 +8613800000000）：", false)
	if err != nil {
		return "", err
	}
	return normalizePhone(phone), nil
}

func (a botCodeAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	for {
		masked, err := a.manager.ask(ctx, a.flow, "code", "验证码已发送，请将收到的登录验证码每位数字 +1 后发送（9 变 0）。例如收到 56789，请发送 67890：", true)
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
	return a.manager.ask(ctx, a.flow, "password", "请输入 2FA 密码：", true)
}

func (a botCodeAuthenticator) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("don't support sign up Telegram account")
}

func (a botCodeAuthenticator) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

func normalizePhone(phone string) string {
	return strings.NewReplacer(" ", "", "\t", "", "-", "", "(", "", ")", "").Replace(phone)
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
