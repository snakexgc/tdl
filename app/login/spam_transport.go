package login

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/tclient"
)

type SpamProbe struct{ Options func() SessionOptions }

func (p SpamProbe) Reply(ctx context.Context, account types.AccountID) (string, error) {
	opts := p.Options()
	if opts.Account != account {
		return "", errors.New("spam transport account mismatch")
	}
	return spamResponse(ctx, opts)
}

func spamResponse(ctx context.Context, opts SessionOptions) (string, error) {
	if opts.KV == nil {
		return "", errors.New("session storage is nil")
	}
	const replyTimeout = 20 * time.Second
	replyCh := make(chan string, 8)
	var spambotID int64

	handler := telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
		id := atomic.LoadInt64(&spambotID)
		if id == 0 {
			return nil
		}
		collectSpambotMessages(u, id, replyCh)
		return nil
	})

	c, err := tclient.New(ctx, tclient.Options{
		Connections: opts.Connections, Credentials: opts.Credentials, Account: opts.Account,
		KV:               opts.KV,
		Proxy:            opts.Proxy,
		NTP:              opts.NTP,
		ReconnectTimeout: opts.ReconnectTimeout,
		UpdateHandler:    handler,
	}, false)
	if err != nil {
		return "", errors.Wrap(err, "create client")
	}

	var response string
	if err := c.Run(ctx, func(ctx context.Context) error {
		resolved, err := c.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: "spambot"})
		if err != nil {
			return errors.Wrap(err, "resolve @spambot")
		}
		var spambotPeer tg.InputPeerClass
		var spambotUID int64
		for _, u := range resolved.Users {
			user, ok := u.AsNotEmpty()
			if !ok {
				continue
			}
			spambotUID = user.ID
			spambotPeer = user.AsInputPeer()
			break
		}
		if spambotPeer == nil {
			return errors.New("could not resolve @spambot")
		}
		atomic.StoreInt64(&spambotID, spambotUID)

		_, err = c.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     spambotPeer,
			Message:  "/start",
			RandomID: time.Now().UnixNano(),
		})
		if err != nil {
			return errors.Wrap(err, "send /start to @spambot")
		}

		timer := time.NewTimer(replyTimeout)
		defer timer.Stop()
		select {
		case reply := <-replyCh:
			response = reply
			return nil
		case <-timer.C:
			return errors.New("timeout waiting for @spambot reply")
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		return "", err
	}
	return response, nil
}

func collectSpambotMessages(u tg.UpdatesClass, spambotID int64, ch chan<- string) {
	var updates []tg.UpdateClass
	switch upd := u.(type) {
	case *tg.Updates:
		updates = upd.Updates
	case *tg.UpdatesCombined:
		updates = upd.Updates
	case *tg.UpdateShort:
		updates = []tg.UpdateClass{upd.Update}
	}
	for _, update := range updates {
		msg, ok := extractUserMessage(update, spambotID)
		if !ok {
			continue
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

func extractUserMessage(update tg.UpdateClass, fromID int64) (string, bool) {
	newMsg, ok := update.(*tg.UpdateNewMessage)
	if !ok {
		return "", false
	}
	msg, ok := newMsg.Message.(*tg.Message)
	if !ok || msg.Out {
		return "", false
	}
	peer, ok := msg.PeerID.(*tg.PeerUser)
	if !ok || peer.UserID != fromID {
		return "", false
	}
	return msg.Message, true
}
