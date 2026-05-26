package forward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/forwarder"
	"github.com/iyear/tdl/core/storage"
	coretclient "github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
	pkgtclient "github.com/iyear/tdl/pkg/tclient"
)

type SessionOptions struct {
	KV               storage.Storage
	Proxy            string
	NTP              string
	ReconnectTimeout time.Duration
	PoolSize         int
	Threads          int
}

type Request struct {
	Links   []string
	To      string
	Mode    string
	Silent  bool
	DryRun  bool
	Grouped bool
}

type Result struct {
	Target    string       `json:"target"`
	Mode      string       `json:"mode"`
	Submitted int          `json:"submitted"`
	Failed    int          `json:"failed"`
	Items     []ResultItem `json:"items"`
}

type ResultItem struct {
	Link      string `json:"link"`
	PeerID    int64  `json:"peer_id,omitempty"`
	MessageID int    `json:"message_id,omitempty"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

func RunLinks(ctx context.Context, opts SessionOptions, req Request) (Result, error) {
	if opts.KV == nil {
		return Result{}, errors.New("session storage is nil")
	}
	if len(req.Links) == 0 {
		return Result{}, errors.New("no Telegram message links to forward")
	}
	if opts.PoolSize < 0 {
		opts.PoolSize = config.DefaultPoolSize
	}
	if opts.Threads <= 0 {
		opts.Threads = config.DefaultThreads
	}
	if !req.Grouped {
		req.Grouped = true
	}

	mode, err := NormalizeMode(req.Mode)
	if err != nil {
		return Result{}, err
	}

	client, err := pkgtclient.New(ctx, pkgtclient.Options{
		KV:               opts.KV,
		Proxy:            opts.Proxy,
		NTP:              opts.NTP,
		ReconnectTimeout: opts.ReconnectTimeout,
	}, false)
	if err != nil {
		return Result{}, errors.Wrap(err, "create client")
	}

	result := Result{
		Target: strings.TrimSpace(req.To),
		Mode:   ConfigModeName(mode),
		Items:  make([]ResultItem, 0, len(req.Links)),
	}

	err = coretclient.RunWithAuth(ctx, client, func(ctx context.Context) (rerr error) {
		pool := dcpool.NewPool(client,
			int64(opts.PoolSize),
			coretclient.NewDefaultMiddlewares(ctx, opts.ReconnectTimeout)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))

		manager := peers.Options{Storage: storage.NewPeers(opts.KV)}.Build(pool.Default(ctx))
		to, err := ResolvePeer(ctx, manager, req.To)
		if err != nil {
			return errors.Wrap(err, "resolve forward target")
		}
		result.Target = to.VisibleName()

		elems := make([]forwarder.Elem, 0, len(req.Links))
		for _, raw := range req.Links {
			link := strings.TrimSpace(raw)
			item := ResultItem{Link: link}
			from, msgID, err := tutil.ParseMessageLink(ctx, manager, link)
			if err != nil {
				item.Error = err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			msg, err := tutil.GetSingleMessage(ctx, pool.Default(ctx), from.InputPeer(), msgID)
			if err != nil {
				item.PeerID = from.ID()
				item.MessageID = msgID
				item.Error = err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}

			item.PeerID = from.ID()
			item.MessageID = msg.ID
			item.OK = true
			result.Submitted++
			result.Items = append(result.Items, item)
			elems = append(elems, NewElem(from, msg, to, ElemOptions{
				Mode:    mode,
				Silent:  req.Silent,
				DryRun:  req.DryRun,
				Grouped: req.Grouped,
			}))
		}
		if len(elems) == 0 {
			return nil
		}

		return Run(ctx, pool, elems, opts.Threads, nil)
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func Run(ctx context.Context, pool dcpool.Pool, elems []forwarder.Elem, threads int, progress forwarder.Progress) error {
	if pool == nil {
		return errors.New("forward pool is nil")
	}
	if progress == nil {
		progress = NopProgress{}
	}
	if threads <= 0 {
		threads = config.DefaultThreads
	}
	fw := forwarder.New(forwarder.Options{
		Pool:     pool,
		Threads:  threads,
		Iter:     NewSliceIter(elems),
		Progress: progress,
	})
	return fw.Forward(ctx)
}

func ForwardSingle(ctx context.Context, pool dcpool.Pool, from peers.Peer, msg *tg.Message, to peers.Peer, opts ElemOptions, threads int) error {
	return Run(ctx, pool, []forwarder.Elem{
		NewElem(from, msg, to, opts),
	}, threads, nil)
}

func ResolvePeer(ctx context.Context, manager *peers.Manager, target string) (peers.Peer, error) {
	if manager == nil {
		return nil, errors.New("peer manager is nil")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return manager.Self(ctx)
	}
	return tutil.GetInputPeer(ctx, manager, target)
}

func NormalizeMode(mode string) (forwarder.Mode, error) {
	normalized, err := config.NormalizeForwardMode(mode)
	if err != nil {
		return forwarder.ModeDirect, err
	}
	switch normalized {
	case config.ForwardModeDefault:
		return forwarder.ModeDirect, nil
	case config.ForwardModeClone:
		return forwarder.ModeClone, nil
	default:
		return forwarder.ModeDirect, fmt.Errorf("unsupported forward mode %q", normalized)
	}
}

func ConfigModeName(mode forwarder.Mode) string {
	switch mode {
	case forwarder.ModeClone:
		return config.ForwardModeClone
	default:
		return config.ForwardModeDefault
	}
}
