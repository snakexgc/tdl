package watch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/core/tmedia"
)

func (w *Watcher) renderTarget(ctx context.Context, root, directoryID string, dialogID int64, peerName string, downloadedAt time.Time, msg, triggerMsg *tg.Message, media *tmedia.Media) (ports.NamingResult, error) {
	if w.opts.Naming == nil {
		return ports.NamingResult{}, fmt.Errorf("naming policy is not initialized")
	}
	if msg == nil || media == nil {
		return ports.NamingResult{}, fmt.Errorf("naming requires message and media metadata")
	}
	if triggerMsg == nil {
		triggerMsg = msg
	}
	albumID := ""
	if groupedID, ok := msg.GetGroupedID(); ok {
		albumID = fmt.Sprint(groupedID)
	}
	return w.opts.Naming.Render(ctx, ports.NamingInput{Account: w.opts.Account, BaseDir: root, Data: ports.NamingData{
		DialogID: dialogID, DirectoryID: directoryID, PeerName: peerName, DownloadedAt: downloadedAt,
		MessageID: msg.ID, MessageDate: int64(msg.Date), Caption: msg.Message,
		TriggerMessageID: triggerMsg.ID, MessageTitle: strings.TrimSpace(triggerMsg.Message),
		AlbumID: albumID, FileName: media.Name, FileSize: media.Size,
	}})
}
