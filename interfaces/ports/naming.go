package ports

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

const NamingRulesName = "naming.rules"

// NamingData contains values only: naming policy never fetches Telegram peers
// or messages and does not access a downloader's filesystem.
type NamingData struct {
	DialogID         int64
	DirectoryID      string
	PeerName         string
	MessageID        int
	TriggerMessageID int
	MessageDate      int64
	Caption          string
	MessageTitle     string
	AlbumID          string
	FileName         string
	FileSize         int64
	DownloadedAt     time.Time
}

type NamingInput struct {
	Account types.AccountID
	BaseDir string
	Data    NamingData
	// RenderedName reuses the filename of an already registered download link.
	// It avoids applying the filename template twice when queuing saved links.
	RenderedName string
}

type NamingResult struct {
	FileName string
	Dir      string
	Out      string
	FullPath string
	MaxBytes int
}

type NamingRules interface {
	Render(context.Context, NamingInput) (NamingResult, error)
	Unique(context.Context, []NamingResult) ([]NamingResult, error)
}
