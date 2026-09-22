package forward

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/snakexgc/tdl/internal/core/forwarder"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
)

// ResolvePeer resolves a forward target string to a peer. An empty target means
// the current user's Saved Messages.
func ResolvePeer(ctx context.Context, manager *peers.Manager, target string) (peers.Peer, error) {
	if manager == nil {
		return nil, errors.New("peer manager is nil")
	}
	target = strings.TrimSpace(target)
	if target == "" || target == "self" {
		return manager.Self(ctx)
	}
	if kind, raw, typed := strings.Cut(target, ":"); typed && (kind == "user" || kind == "chat" || kind == "channel") {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid chat reference %q", target)
		}
		switch kind {
		case "user":
			return manager.ResolveUserID(ctx, id)
		case "chat":
			return manager.ResolveChatID(ctx, id)
		case "channel":
			return manager.ResolveChannelID(ctx, id)
		}
	}
	return tutil.GetInputPeer(ctx, manager, target)
}

// NormalizeMode converts a config forward mode name into a forwarder.Mode.
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

// ConfigModeName converts a forwarder.Mode back into its config mode name.
func ConfigModeName(mode forwarder.Mode) string {
	switch mode {
	case forwarder.ModeClone:
		return config.ForwardModeClone
	default:
		return config.ForwardModeDefault
	}
}
