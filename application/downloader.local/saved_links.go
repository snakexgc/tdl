package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/targetpath"
)

// SavedLinks queues existing sources using the owning account's live naming
// policy. It does not create a replacement policy or start a transfer worker.
type SavedLinks struct {
	Account    types.AccountID
	Root       string
	Naming     ports.NamingRules
	Source     ports.SavedDownloadLinks
	Repository ports.LocalLinkRepository
}

func (SavedLinks) Name() string { return "local" }

func (s SavedLinks) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	if in.Account != s.Account {
		return types.DownloadResult{}, fmt.Errorf("download account mismatch")
	}
	id := strings.TrimSpace(in.TaskID)
	if id == "" || id == "index" || strings.ContainsAny(id, `/\`) {
		return types.DownloadResult{}, fmt.Errorf("invalid download task id")
	}
	if s.Source == nil || s.Repository == nil || s.Naming == nil {
		return types.DownloadResult{}, fmt.Errorf("saved download resources are unavailable")
	}
	source, ok, err := s.Source.GetLink(ctx, id)
	if err != nil {
		return types.DownloadResult{}, err
	}
	if !ok {
		return types.DownloadResult{}, fmt.Errorf("download link record not found")
	}
	task := source.Task
	if task.ID != id {
		return types.DownloadResult{}, fmt.Errorf("download link identity mismatch")
	}
	root, err := PrepareRoot(s.Root)
	if err != nil {
		return types.DownloadResult{}, err
	}
	directory := strconv.FormatInt(task.PeerID, 10)
	if task.PeerID == 0 {
		directory = task.ID
	}
	target, err := s.Naming.Render(ctx, ports.NamingInput{Account: s.Account, BaseDir: root, RenderedName: task.FileName, Data: ports.NamingData{
		DialogID: task.PeerID, DirectoryID: directory, PeerName: targetpath.SafePathSegment(directory), MessageID: task.MessageID, TriggerMessageID: task.MessageID, FileName: task.FileName, FileSize: task.FileSize, DownloadedAt: time.Now(),
	}})
	if err != nil {
		return types.DownloadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	record, err := s.Repository.CreateLinked(ctx, source, types.LocalDownloadRecord{ID: id, TaskID: id, FileName: task.FileName, Dir: target.Dir, Out: target.Out, Path: target.FullPath, Total: task.FileSize, Status: types.LocalDownloadStatusQueued, CreatedAt: time.Now()})
	if err != nil {
		return types.DownloadResult{}, err
	}
	return types.DownloadResult{Account: s.Account, Target: s.Name(), ID: record.ID}, nil
}

// PrepareRoot validates the configured local destination before queueing work.
func PrepareRoot(configured string) (string, error) {
	root := strings.TrimSpace(configured)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("local download root must be absolute")
	}
	root = filepath.Clean(root)
	if err := EnsureWritableDirectory(root); err != nil {
		return "", fmt.Errorf("prepare local download directory %q: %w", root, err)
	}
	return root, nil
}

func EnsureWritableDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tdl-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
