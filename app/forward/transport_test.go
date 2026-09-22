package forward

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/dcpool"
	"github.com/snakexgc/tdl/internal/core/forwarder"
)

type failingInvoker struct{ err error }

func (f failingInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error { return f.err }

type transportPool struct {
	dcpool.Pool
	client *tg.Client
}

func (p transportPool) Default(context.Context) *tg.Client { return p.client }

type transportPeer struct{ peers.Peer }

func (transportPeer) ID() int64                    { return 1 }
func (transportPeer) InputPeer() tg.InputPeerClass { return &tg.InputPeerSelf{} }
func (transportPeer) VisibleName() string          { return "test peer" }

func TestForwardTransportReportsMessageAndAlbumFailures(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(map[bool]string{false: "message", true: "album"}[grouped], func(t *testing.T) {
			failure := errors.New("telegram unavailable")
			pool := transportPool{client: tg.NewClient(failingInvoker{failure})}
			message := &tg.Message{ID: 1, Message: "hello"}
			if grouped {
				message.SetGroupedID(10)
			}
			peer := transportPeer{}
			elem := NewElem(peer, message, peer, ElemOptions{Mode: forwarder.ModeDirect, Grouped: grouped})
			progress := &jobProgress{ctx: context.Background(), job: &Job{}, report: func(Job) {}}
			worker := forwarder.New(forwarder.Options{Pool: pool, Threads: 1, Iter: NewSliceIter([]forwarder.Elem{elem}), Progress: progress})
			require.ErrorIs(t, worker.Forward(context.Background()), failure)
		})
	}
}
