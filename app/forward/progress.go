package forward

import "github.com/iyear/tdl/core/forwarder"

type NopProgress struct{}

func (NopProgress) OnAdd(forwarder.Elem) {}

func (NopProgress) OnClone(forwarder.Elem, forwarder.ProgressState) {}

func (NopProgress) OnDone(forwarder.Elem, error) {}
