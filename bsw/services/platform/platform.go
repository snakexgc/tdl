// Package platform adapts process build metadata and network facilities during
// migration. Linker variables keep their historical paths for release builds.
package platform

import (
	"golang.org/x/net/proxy"

	"github.com/snakexgc/tdl/internal/core/util/netutil"
	"github.com/snakexgc/tdl/pkg/consts"
)

type BuildInfo struct{ Version, Commit, Date, GOARM string }

func Build() BuildInfo {
	return BuildInfo{Version: consts.Version, Commit: consts.Commit, Date: consts.CommitDate, GOARM: consts.GOARM}
}

func Proxy(raw string) (proxy.ContextDialer, error) { return netutil.NewProxy(raw) }
