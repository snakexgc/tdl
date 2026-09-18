package tgauth

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/gotd/td/telegram/updates"

	"github.com/snakexgc/tdl/bsw/services/nvm"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type AccessHashes struct{ store storage.Storage }

var (
	_ updates.ChannelAccessHasher = (*AccessHashes)(nil)
	_ updates.UserAccessHasher    = (*AccessHashes)(nil)
)

func NewAccessHashes(store storage.Storage) (*AccessHashes, error) {
	registry := nvm.New(store)
	const name = "account.telegram.access_hash"
	if err := registry.Register(nvm.Dataset{Name: name, Writer: protocolOwner, Prefix: "access_hash:"}); err != nil {
		return nil, err
	}
	scoped, err := registry.Writer(name, protocolOwner)
	if err != nil {
		return nil, err
	}
	return &AccessHashes{store: scoped}, nil
}

func hashKey(kind string, userID, targetID int64) string {
	return fmt.Sprintf("access_hash:%s:%d:%d", kind, userID, targetID)
}

func (h *AccessHashes) get(ctx context.Context, key string) (int64, bool, error) {
	data, err := h.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	value, err := strconv.ParseInt(string(data), 10, 64)
	return value, err == nil, err
}

func (h *AccessHashes) SetChannelAccessHash(ctx context.Context, userID, channelID, hash int64) error {
	return h.store.Set(ctx, hashKey("channel", userID, channelID), []byte(strconv.FormatInt(hash, 10)))
}

func (h *AccessHashes) GetChannelAccessHash(ctx context.Context, userID, channelID int64) (int64, bool, error) {
	return h.get(ctx, hashKey("channel", userID, channelID))
}

func (h *AccessHashes) SetUserAccessHash(ctx context.Context, userID, targetID, hash int64) error {
	return h.store.Set(ctx, hashKey("user", userID, targetID), []byte(strconv.FormatInt(hash, 10)))
}

func (h *AccessHashes) GetUserAccessHash(ctx context.Context, userID, targetID int64) (int64, bool, error) {
	return h.get(ctx, hashKey("user", userID, targetID))
}
