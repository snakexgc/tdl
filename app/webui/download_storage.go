package webui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/services/localfs"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/targetpath"
)

func (s *Server) handleDownloadStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	data, err := s.downloadStorageSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) downloadStorageSnapshot(ctx context.Context) (any, error) {
	cfg := config.From(s.opts.Context)
	if config.PrimaryDownloadExecutor(cfg) != config.DownloadExecutorLocal {
		return nil, fmt.Errorf("local downloader is not selected")
	}
	root, err := targetpath.LocalRoot(cfg.Downloader.LocalRoot)
	if err != nil {
		return nil, err
	}
	return s.storageSamples.Read(ctx, root, func(ctx context.Context) (any, error) {
		records, err := taskhub.NewLocalRepository(s.opts.NamespaceKV).Records(ctx)
		if err != nil {
			return nil, err
		}
		var incomplete []string
		for _, record := range records {
			if record.Status != types.LocalDownloadStatusComplete && record.Path != "" {
				incomplete = append(incomplete, record.Path)
			}
		}
		return localfs.DownloadUsage(ctx, root, incomplete), nil
	})
}
