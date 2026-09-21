package namingrules

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type namingClock struct{ at time.Time }

func (c namingClock) Now() time.Time          { return c.at }
func (namingClock) Status() types.ClockStatus { return types.ClockStatus{Synchronized: true} }

func TestNamingTemplatesFollowTheRTEClock(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {
		filenameField: `{{ formatDate now "2006" }}-{{ .F }}`, directoryField: "Y/M",
	}})
	require.NoError(t, err)
	host.Start(ctx)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.NamingRulesName)
	require.NoError(t, err)
	for _, year := range []int{2030, 2031} {
		at := time.Date(year, 2, 3, 4, 5, 6, 0, time.UTC)
		binding.Bind(namingClock{at})
		result, err := value.(ports.NamingRules).Render(context.Background(), ports.NamingInput{BaseDir: testDownloadsRoot, Data: ports.NamingData{FileName: "video.mp4"}})
		require.NoError(t, err)
		require.Equal(t, at.Format("2006")+"-video.mp4", result.Out)
		require.Equal(t, testDownloadsRoot+"/"+at.Format("2006/01"), result.Dir)
	}
}
