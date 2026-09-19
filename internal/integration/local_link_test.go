package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	httpdl "github.com/snakexgc/tdl/app/http"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

type namingDuringSourceChange struct {
	ports.NamingRules
	change func()
}

func (n namingDuringSourceChange) Render(_ context.Context, in ports.NamingInput) (ports.NamingResult, error) {
	n.change()
	return ports.NamingResult{Dir: in.BaseDir, Out: in.RenderedName, FullPath: filepath.Join(in.BaseDir, in.RenderedName)}, nil
}

func TestSavedLocalLinkCannotResurrectDeletedOrReplacedSource(t *testing.T) {
	for _, action := range []string{testDeleteAction, "replace"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "state")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			storage, err := engine.Open(string(types.DefaultAccount))
			require.NoError(t, err)
			const source = `{"id":"source","peer_id":10,"file_name":"file.bin","file_size":42,"media":{"name":"file.bin","size":42,"dc":2,"location":{"kind":"document","id":42,"access_hash":99}}}`
			require.NoError(t, storage.Set(ctx, taskhub.LinkPrefix+testLinkSource, []byte(source)))
			repository := taskhub.NewLocalRepository(storage)
			naming := namingDuringSourceChange{change: func() {
				if action == testDeleteAction {
					require.NoError(t, taskhub.Links(storage).Remove(ctx, testLinkSource))
				} else {
					require.NoError(t, storage.Set(ctx, taskhub.LinkPrefix+testLinkSource, []byte(`{"id":"source","file_name":"replacement.bin"}`)))
				}
			}}
			queue := local.SavedLinks{Account: types.DefaultAccount, Root: t.TempDir(), Source: httpdl.NewTaskStore(storage, 0), Repository: repository, Naming: naming}
			_, err = queue.Submit(ctx, types.DownloadSubmission{Account: types.DefaultAccount, TaskID: testLinkSource})
			require.Error(t, err, "stale source must not enter the local queue")
			records, err := repository.Records(ctx)
			require.NoError(t, err)
			require.Empty(t, records)
		})
	}
}
