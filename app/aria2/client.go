package aria2

import (
	"github.com/snakexgc/tdl/bsw/ecual/aria2rpc"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	Client         = aria2rpc.Client
	AddURIOptions  = types.Aria2AddURIOptions
	DownloadStatus = types.Aria2DownloadStatus
	BT             = types.Aria2BT
	BTInfo         = types.Aria2BTInfo
	File           = types.Aria2File
	URI            = types.Aria2URI
)

var (
	NewClient         = aria2rpc.NewClient
	IsConnectionError = aria2rpc.IsConnectionError
)
