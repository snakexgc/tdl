package forwarder

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type CommandSettings struct {
	Target, Mode string
	Silent       bool
}

// Command owns forwarding decisions and only exchanges plain console DTOs.
// Each connection gets a new handle; a stopped handle cannot enqueue work.
type Command struct {
	account  types.AccountID
	queue    ports.ForwardTasks
	validate LinkValidator
	settings func() CommandSettings
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	closed   bool
	active   sync.WaitGroup
}

func NewCommand(ctx context.Context, account types.AccountID, queue ports.ForwardTasks, validate LinkValidator, settings func() CommandSettings) *Command {
	ctx, cancel := context.WithCancel(ctx)
	return &Command{account: account, queue: queue, validate: validate, settings: settings, ctx: ctx, cancel: cancel}
}

func (c *Command) Stop(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Command) Execute(ctx context.Context, request types.ConsoleRequest) (types.ConsoleResponse, error) {
	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		return types.ConsoleResponse{}, fmt.Errorf("forward command is stopped")
	}
	c.active.Add(1)
	c.mu.Unlock()
	defer c.active.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := ctx.Err(); err != nil {
		return types.ConsoleResponse{}, err
	}
	if request.Account != c.account {
		return types.ConsoleResponse{}, fmt.Errorf("forward command account mismatch")
	}
	if request.Name != forwardCommandName {
		return types.ConsoleResponse{}, fmt.Errorf("unsupported forward command %q", request.Name)
	}
	if !request.Private {
		return types.ConsoleResponse{Text: "请在私聊中发送控制命令。"}, nil
	}
	if c.queue == nil || c.validate == nil || c.settings == nil {
		return types.ConsoleResponse{}, fmt.Errorf("forward command resources are unavailable")
	}
	args := strings.Fields(request.Text)
	if len(args) > 2 {
		return types.ConsoleResponse{Text: forwardCommandUsage}, nil
	}
	settings := c.settings() // Keep one policy snapshot for the entire request.
	if settings.Mode == "" {
		settings.Mode = forwardModeDefault
	}
	if settings.Mode != forwardModeDefault && settings.Mode != forwardModeClone {
		return types.ConsoleResponse{}, fmt.Errorf("invalid forward mode %q", settings.Mode)
	}
	if len(args) == 2 {
		settings.Target = args[1]
	}
	var links []string
	seen := map[string]bool{}
	for _, field := range strings.Fields(request.ReplyText) {
		if err := ctx.Err(); err != nil {
			return types.ConsoleResponse{}, err
		}
		link, err := c.validate(ctx, c.account, strings.Trim(field, "<>()[]{}\"'.,;，。；"))
		if err == nil && !seen[link] {
			seen[link] = true
			links = append(links, link)
		}
	}
	if err := ctx.Err(); err != nil {
		return types.ConsoleResponse{}, err
	}
	if len(links) == 0 {
		return types.ConsoleResponse{Text: forwardCommandUsage}, nil
	}
	ids, err := c.queue.EnqueueLinks(ctx, links, settings.Target, "", settings.Mode, settings.Silent)
	if err != nil {
		// A later persistence failure does not undo accepted earlier links.
		return types.ConsoleResponse{Text: fmt.Sprintf("已加入转发队列：%d 条；后续入队失败：%v。请在转发监控核对已入队任务，避免重复提交。", len(ids), err)}, nil
	}
	return types.ConsoleResponse{Text: fmt.Sprintf("已加入转发队列：%d 条，将按顺序逐个转发。可在 Web 管理面板的「转发监控」查看进度与暂停/继续/删除。", len(ids))}, nil
}

const forwardCommandUsage = "用法：回复一条包含 Telegram 消息链接的消息，发送 /forward [目标]。\n目标不填时使用 forwarder.target；目标为空时转发到收藏夹。"
