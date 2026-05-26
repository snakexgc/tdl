package aria2

import (
	"context"
	"testing"
	"time"

	gferrors "github.com/go-faster/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testGIDPauseLow = "pause-low"
	testGIDPauseMid = "pause-mid"
	testGIDOnly     = "only"
)

func TestTelegramErrorRegulatorPausesExtraActiveTasksTemporarily(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newAria2TaskStore(newMemoryTaskStorage())
	now := time.Now()
	for _, gid := range []string{"keep", testGIDPauseMid, testGIDPauseLow} {
		require.NoError(t, store.Add(ctx, aria2TaskRecord{
			GID:       gid,
			TaskID:    "document_" + gid,
			CreatedAt: now,
		}))
	}

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: testGIDPauseLow, Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "100"},
			{GID: "keep", Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "900"},
			{GID: testGIDPauseMid, Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "200"},
		},
	}
	regulator := newTelegramErrorRegulator(client, store, "http://127.0.0.1:8080", zap.NewNop(), telegramErrorRegulatorConfig{
		PauseDuration: time.Millisecond,
		ActionTimeout: time.Second,
	})

	err := regulator.regulate(ctx, gferrors.New("telegram file error"))
	require.NoError(t, err)
	require.Equal(t, []string{"pause-mid", testGIDPauseLow}, client.forcePaused)
	require.Equal(t, []string{"pause-mid", testGIDPauseLow}, client.unpaused)
}

func TestTelegramErrorRegulatorRestartsOnlyActiveTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newAria2TaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:       testGIDOnly,
		TaskID:    "document_only",
		CreatedAt: time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: testGIDOnly, Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "500"},
		},
	}
	regulator := newTelegramErrorRegulator(client, store, "http://127.0.0.1:8080", zap.NewNop(), telegramErrorRegulatorConfig{
		PauseDuration: time.Millisecond,
		ActionTimeout: time.Second,
	})

	err := regulator.regulate(ctx, gferrors.New("telegram file error"))
	require.NoError(t, err)
	require.Equal(t, []string{testGIDOnly}, client.forcePaused)
	require.Equal(t, []string{testGIDOnly}, client.unpaused)
}

func TestTelegramErrorRegulatorRecordErrorThresholdAndCooldown(t *testing.T) {
	t.Parallel()

	regulator := newTelegramErrorRegulator(&fakeAria2ReconnectClient{}, newAria2TaskStore(newMemoryTaskStorage()), "http://127.0.0.1:8080", zap.NewNop(), telegramErrorRegulatorConfig{
		Window:    time.Second,
		Threshold: 2,
		Cooldown:  time.Hour,
	})
	current := time.Unix(100, 0)
	regulator.now = func() time.Time { return current }

	require.False(t, regulator.recordError(gferrors.New("first")))
	require.True(t, regulator.recordError(gferrors.New("second")))
	regulator.finishRegulation()

	current = current.Add(time.Second)
	require.False(t, regulator.recordError(gferrors.New("cooldown-1")))
	require.False(t, regulator.recordError(gferrors.New("cooldown-2")))
}
