package ports

import "context"

const NetworkProxyName = "network.proxy"

// NetworkProxy supplies the current shared outbound proxy, including credentials.
// Implementations must not expose the result through public status responses.
type NetworkProxy interface {
	Proxy(context.Context) (string, error)
}
