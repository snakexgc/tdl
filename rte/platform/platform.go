package platform

import (
	"golang.org/x/net/proxy"

	"github.com/snakexgc/tdl/bsw/services/platform"
)

type BuildInfo = platform.BuildInfo

func BuildMetadata() BuildInfo                            { return platform.Build() }
func ProxyDialer(raw string) (proxy.ContextDialer, error) { return platform.Proxy(raw) }
