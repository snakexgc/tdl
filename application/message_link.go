package application

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

// ValidateMessageLink is the legacy command/UI adapter into the component port.
// The temporary host has no network or storage resources.
func ValidateMessageLink(ctx context.Context, account types.AccountID, raw string) (string, error) {
	registry, err := Registry()
	if err != nil {
		return "", err
	}
	host, err := registry.Build(account, map[string]bool{"trigger.messagelink": true}, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = host.Stop(ctx) }()
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			return "", fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.MessageLinksName)
	if err != nil {
		return "", err
	}
	return value.(ports.MessageLinks).Validate(ctx, account, raw)
}
