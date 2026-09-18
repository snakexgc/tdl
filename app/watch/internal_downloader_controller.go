package watch

import (
	"context"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/snakexgc/tdl/app/http"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type InternalDownloadController struct {
	*local.Controller
	store *internalTaskStore
	kv    storage.Storage
}

func NewInternalDownloadController(kvd storage.Storage) *InternalDownloadController {
	store := newInternalTaskStore(kvd)
	return &InternalDownloadController{Controller: local.NewController(store), store: store, kv: kvd}
}

func (c *InternalDownloadController) AddLink(ctx context.Context, cfg *config.Config, taskID string) (InternalDownloadInfo, error) {
	if c == nil || c.kv == nil {
		return InternalDownloadInfo{}, errors.New("namespace kv storage is not configured")
	}
	if cfg == nil {
		cfg = config.Get()
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || strings.Contains(taskID, "/") {
		return InternalDownloadInfo{}, errors.New("invalid download task id")
	}

	task, ok, err := httpdl.NewTaskStore(c.kv, 0).Get(ctx, taskID)
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	if !ok {
		return InternalDownloadInfo{}, errors.New("download link record not found")
	}

	root, _, err := prepareInternalOutputRoot(cfg)
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	data := internalDownloadDirData(task)
	policies, _, naming, err := startPolicies(ctx, cfg.Namespace, DefaultOptions(cfg))
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	defer func() { _ = policies.Stop(context.Background()) }()
	target, err := naming.Render(ctx, ports.NamingInput{Account: types.AccountID(cfg.Namespace), BaseDir: root, RenderedName: task.FileName, Data: ports.NamingData{
		DirectoryID: data.ID, PeerName: data.Name, MessageID: task.MessageID, TriggerMessageID: task.MessageID, FileName: task.FileName, DownloadedAt: data.Time,
	}})
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	dir, out, fullPath := target.Dir, target.Out, target.FullPath
	record := internalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       dir,
		Out:       out,
		Path:      fullPath,
		Total:     task.FileSize,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}
	record, err = c.store.Create(ctx, record)
	if err != nil {
		return InternalDownloadInfo{}, err
	}

	return internalDownloadInfo(record), nil
}
