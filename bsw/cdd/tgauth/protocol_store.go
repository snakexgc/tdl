package tgauth

import (
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"

	"github.com/snakexgc/tdl/bsw/services/nvm"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const protocolOwner = "tgauth.protocol"

// ProtocolStore retains legacy key names and grants only protocol metadata.
// Session credentials and task datasets are deliberately separate capabilities.
func ProtocolStore(store storage.Storage) (storage.Storage, error) {
	registry := nvm.New(store)
	names := []string{"peers:", "state:", "chan:"}
	for _, prefix := range names {
		if err := registry.Register(nvm.Dataset{Name: prefix, Writer: protocolOwner, Prefix: prefix}); err != nil {
			return nil, err
		}
	}
	return registry.WriterSet(protocolOwner, names...)
}

func PeersStore(store storage.Storage) (peers.Storage, error) {
	registry := nvm.New(store)
	const dataset = "account.telegram.peers"
	if err := registry.Register(nvm.Dataset{Name: dataset, Writer: protocolOwner, Prefix: "peers:"}); err != nil {
		return nil, err
	}
	scoped, err := registry.Writer(dataset, protocolOwner)
	if err != nil {
		return nil, err
	}
	return storage.NewPeers(scoped), nil
}

func UpdateStore(store storage.Storage) (updates.StateStorage, error) {
	registry := nvm.New(store)
	for _, prefix := range []string{"state:", "chan:"} {
		if err := registry.Register(nvm.Dataset{Name: prefix, Writer: protocolOwner, Prefix: prefix}); err != nil {
			return nil, err
		}
	}
	scoped, err := registry.WriterSet(protocolOwner, "state:", "chan:")
	if err != nil {
		return nil, err
	}
	return storage.NewState(scoped), nil
}
