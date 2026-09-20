package taskhub

import (
	"github.com/snakexgc/tdl/bsw/services/nvm"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const writerID = "taskhub"

// Hub is the composition boundary for task dataset ownership. Application code
// receives repository methods, never NvM writer handles.
type Hub struct {
	Forward *Collection
	Links   *Collection
	Aria2   *Collection
	Local   *Collection
}

func Open(store storage.Storage) (*Hub, error) {
	registry := nvm.New(store)
	hub := &Hub{}
	for _, item := range []struct {
		name, scope, prefix, index string
		target                     **Collection
	}{
		{"forward", "forward.", ForwardPrefix, ForwardIndex, &hub.Forward},
		{"links", LinkPrefix, LinkPrefix, LinkIndex, &hub.Links},
		{"aria2", "watch.aria2.", Aria2Prefix, Aria2Index, &hub.Aria2},
		{"local", "download.local.", LocalPrefix, LocalIndex, &hub.Local},
	} {
		if err := registry.Register(nvm.Dataset{Name: item.name, Writer: writerID, Prefix: item.scope}); err != nil {
			return nil, err
		}
		writer, err := registry.Writer(item.name, writerID)
		if err != nil {
			return nil, err
		}
		*item.target = New(writer, item.prefix, item.index)
	}
	return hub, nil
}
