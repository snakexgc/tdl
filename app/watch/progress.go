package watch

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"go.uber.org/atomic"

	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/utils"
)

// watchProgress manages go-pretty progress trackers for the watch command.
type watchProgress struct {
	pw       pw.Writer
	trackers *sync.Map // map[msgID]*pw.Tracker
}

func newWatchProgress(pw pw.Writer) *watchProgress {
	return &watchProgress{
		pw:       pw,
		trackers: &sync.Map{},
	}
}

// OnAdd creates a new progress tracker for a file being downloaded.
func (p *watchProgress) OnAdd(msgID int, name string, size int64) {
	message := formatProgressMessage(msgID, name)
	tracker := prog.AppendTracker(p.pw, utils.Byte.FormatBinaryBytes, message, size)
	p.trackers.Store(msgID, tracker)
}

// OnDownload updates the progress of a file being downloaded.
func (p *watchProgress) OnDownload(msgID int, downloaded int64, total int64) {
	t, ok := p.trackers.Load(msgID)
	if !ok {
		return
	}
	tracker := t.(*pw.Tracker)
	tracker.UpdateTotal(total)
	tracker.SetValue(downloaded)
}

// OnDone handles download completion or failure.
func (p *watchProgress) OnDone(msgID int, err error) {
	t, ok := p.trackers.Load(msgID)
	if !ok {
		return
	}
	tracker := t.(*pw.Tracker)

	if err != nil && !errors.Is(err, context.Canceled) {
		p.pw.Log(color.RedString("%s error: %s", tracker.Message, err.Error()))
		tracker.MarkAsErrored()
		return
	}
	// Mark as done — go-pretty will show green "done!"
}

// formatProgressMessage creates the message string shown in the progress bar.
// Format: "msgID -> fileName" (matching dl's style: "peer(msgID) -> fileName")
func formatProgressMessage(msgID int, name string) string {
	// Strip .tmp extension if present
	displayName := strings.TrimSuffix(name, tempExt)
	return fmt.Sprintf("%d -> %s", msgID, displayName)
}

// progressWriteAt wraps an *os.File to report download progress via watchProgress.
// This is a lightweight version of core/downloader.writeAt that doesn't require
// the full Elem interface — just a file, a size, and a progress callback.
type progressWriteAt struct {
	file       *os.File
	progress   *watchProgress
	msgID      int
	totalSize  int64
	partSize   int
	downloaded *atomic.Int64
}

func newProgressWriteAt(file *os.File, progress *watchProgress, msgID int, totalSize int64) *progressWriteAt {
	return &progressWriteAt{
		file:       file,
		progress:   progress,
		msgID:      msgID,
		totalSize:  totalSize,
		partSize:   1024 * 1024, // MaxPartSize
		downloaded: atomic.NewInt64(0),
	}
}

func (w *progressWriteAt) WriteAt(p []byte, off int64) (int, error) {
	n, err := w.file.WriteAt(p, off)
	if err != nil {
		return 0, err
	}

	// Small files may finish too fast for the progress bar to render.
	if n < w.partSize {
		time.Sleep(time.Millisecond * 200)
	}

	w.progress.OnDownload(w.msgID, w.downloaded.Add(int64(n)), w.totalSize)
	return n, nil
}
