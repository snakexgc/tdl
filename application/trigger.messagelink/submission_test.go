package messagelink

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type messageSource struct {
	calls   int
	account types.AccountID
}

func (s *messageSource) Resolve(_ context.Context, _ types.AccountID, _ string) (types.DownloadIntent, error) {
	s.calls++
	return types.DownloadIntent{Account: s.account, MessageID: 12, PeerID: 34}, nil
}

type requestSink struct{ requests []types.DownloadIntent }

func (s *requestSink) Submit(_ context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	s.requests = append(s.requests, request)
	return types.DownloadSubmissionSummary{Link: request.Link, Queued: 1}, nil
}

func TestMessageLinkSubmissionUsesAccountBoundResources(t *testing.T) {
	ctx := context.Background()
	validator := &Validator{account: types.DefaultAccount}
	source := &messageSource{account: types.DefaultAccount}
	sink := &requestSink{}
	_, err := validator.Submit(ctx, "other", "https://t.me/c/123/12", source, sink)
	require.Error(t, err)
	require.Zero(t, source.calls)
	_, err = validator.Submit(ctx, types.DefaultAccount, "https://example.com/file", source, sink)
	require.Error(t, err)
	require.Zero(t, source.calls)
	result, err := validator.Submit(ctx, types.DefaultAccount, "https://t.me/c/123/12", source, sink)
	require.NoError(t, err)
	require.Equal(t, 1, result.Queued)
	require.Len(t, sink.requests, 1)
	require.Equal(t, "message_link", sink.requests[0].Source)
	source.account = "other"
	_, err = validator.Submit(ctx, types.DefaultAccount, "https://t.me/c/123/12", source, sink)
	require.ErrorContains(t, err, "source account mismatch")
	require.Len(t, sink.requests, 1)
}
