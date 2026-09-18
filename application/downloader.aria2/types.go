package aria2

import (
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	DownloadStatus      = types.Aria2DownloadStatus
	AddURIOptions       = types.Aria2AddURIOptions
	BTInfo              = types.Aria2BTInfo
	BT                  = types.Aria2BT
	File                = types.Aria2File
	URI                 = types.Aria2URI
	TaskRecord          = types.Aria2TaskRecord
	TaskInfo            = types.Aria2TaskInfo
	Overview            = types.Aria2Overview
	ActionResult        = types.Aria2ActionResult
	aria2DownloadStatus = DownloadStatus
	aria2File           = File
	aria2URI            = URI
	aria2TaskRecord     = TaskRecord
	aria2AddURIOptions  = AddURIOptions
	ControlClient       = ports.Aria2ControlClient
)
