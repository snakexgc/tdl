package forward

import (
	"context"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/forwarder"
)

type ElemOptions struct {
	Mode    forwarder.Mode
	Silent  bool
	DryRun  bool
	Grouped bool
	Thread  int
}

type Elem struct {
	from peers.Peer
	msg  *tg.Message
	to   peers.Peer
	opts ElemOptions
}

func NewElem(from peers.Peer, msg *tg.Message, to peers.Peer, opts ElemOptions) *Elem {
	if !opts.Mode.IsValid() {
		opts.Mode = forwarder.ModeDirect
	}
	return &Elem{
		from: from,
		msg:  msg,
		to:   to,
		opts: opts,
	}
}

func (e *Elem) Mode() forwarder.Mode { return e.opts.Mode }

func (e *Elem) From() peers.Peer { return e.from }

func (e *Elem) Msg() *tg.Message { return e.msg }

func (e *Elem) To() peers.Peer { return e.to }

func (e *Elem) Thread() int { return e.opts.Thread }

func (e *Elem) AsSilent() bool { return e.opts.Silent }

func (e *Elem) AsDryRun() bool { return e.opts.DryRun }

func (e *Elem) AsGrouped() bool { return e.opts.Grouped }

type SliceIter struct {
	elems []forwarder.Elem
	idx   int
}

func NewSliceIter(elems []forwarder.Elem) *SliceIter {
	return &SliceIter{elems: elems}
}

func (i *SliceIter) Next(context.Context) bool {
	if i.idx >= len(i.elems) {
		return false
	}
	i.idx++
	return true
}

func (i *SliceIter) Value() forwarder.Elem {
	if i.idx == 0 || i.idx > len(i.elems) {
		return nil
	}
	return i.elems[i.idx-1]
}

func (i *SliceIter) Err() error {
	return nil
}
