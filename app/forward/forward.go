package forward

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/iyear/tdl/core/forwarder"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

// ResolvePeer resolves a forward target string to a peer. An empty target means
// the current user's Saved Messages.
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
