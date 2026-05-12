package watch

import (
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/watch/aria2"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

type (
	Aria2Controller     = aria2.Controller
	Aria2TaskInfo       = aria2.TaskInfo
	Aria2Overview       = aria2.Overview
	Aria2ActionResult   = aria2.ActionResult
	Aria2DownloadStatus = aria2.DownloadStatus
	Aria2AddURIOptions  = aria2.AddURIOptions
)

func NewAria2Controller(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Aria2Controller {
	return aria2.NewController(cfg, kvd, logger)
}

func Aria2TaskName(task Aria2DownloadStatus) string {
	return aria2.TaskName(task)
}

func Aria2TaskInfoFromStatus(task Aria2DownloadStatus) Aria2TaskInfo {
	return aria2.TaskInfoFromStatus(task)
}
