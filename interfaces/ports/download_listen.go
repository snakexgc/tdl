package ports

import (
	"context"
	"errors"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

var (
	ErrListenNoDiscussion = errors.New("帖子没有可访问的评论区")
	ErrListenInaccessible = errors.New("监听来源不可访问")
	ErrListenDeleted      = errors.New("消息已删除或不可访问")
)

type ListenFloodWait struct {
	Wait time.Duration
	Err  error
}

func (e *ListenFloodWait) Error() string { return e.Err.Error() }
func (e *ListenFloodWait) Unwrap() error { return e.Err }

const (
	DownloadListenName       = "download.listen"
	DownloadListenStatusName = "download.listen.status"
)

type ListenRepository interface {
	Load(context.Context) (types.ListenState, error)
	Update(context.Context, types.ListenMutation) error
}

type ListenTransport interface {
	ResolveSource(context.Context, types.ChatRef, bool) (types.ListenPeer, error)
	History(context.Context, types.MessagePeer, int, int) (types.ListenPage, error)
	Discussion(context.Context, types.MessagePeer, int) (types.ListenThreadRecord, error)
	Replies(context.Context, types.MessagePeer, int, int, int) (types.ListenPage, error)
	Message(context.Context, types.MessagePeer, int) (types.ListenMessage, error)
}

// ListenResources belongs to one authenticated connection and is drained by Serve.
type ListenResources struct {
	Transport ListenTransport
	Downloads DownloadRequests
	Enabled   func() bool
	Accepted  func(context.Context, string) (bool, error)
}

type DownloadListener interface {
	Observe(context.Context, types.ListenMessage) error
	Serve(context.Context, ListenResources) error
}
