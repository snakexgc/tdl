package aria2

import (
	"time"

	"go.uber.org/zap"

	component "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type (
	Controller        = component.Controller
	Aria2Controller   = Controller
	TaskInfo          = component.TaskInfo
	Aria2TaskInfo     = TaskInfo
	Overview          = component.Overview
	Aria2Overview     = Overview
	ActionResult      = component.ActionResult
	Aria2ActionResult = ActionResult
	ControlClient     = ports.Aria2ControlClient
)

func componentOptions(cfg *config.Config, kvd storage.Storage) component.Options {
	if cfg == nil {
		cfg = config.Get()
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return component.Options{Account: types.AccountID(cfg.Namespace), Client: NewClient(cfg.Aria2), Store: NewTaskStore(kvd, downloadLinkTTL(cfg.HTTP)), PublicBaseURL: cfg.HTTP.PublicBaseURL, Connections: cfg.PoolSize, Limit: cfg.Limit}
}

func NewController(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Controller {
	return component.NewController(componentOptions(cfg, kvd), logger)
}

func downloadLinkTTL(cfg config.HTTPConfig) time.Duration {
	if cfg.DownloadLinkTTLHours <= 0 {
		return 0
	}
	return time.Duration(cfg.DownloadLinkTTLHours) * time.Hour
}

var (
	TaskName           = component.TaskName
	TaskInfoFromStatus = component.TaskInfoFromStatus
)
