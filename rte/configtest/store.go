// Package configtest provides an isolated repository for component lifecycle tests.
package configtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/snakexgc/tdl/rte/config"
)

type Repository struct {
	mu        sync.Mutex
	documents map[string][]byte
	SaveError error
}

func NewStore() *config.Store { return config.NewManaged(&Repository{}) }

func (r *Repository) Load(ctx context.Context, id string) (config.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return config.Document{}, err
	}
	doc := config.Document{Version: config.CurrentVersion, Enabled: true, Values: map[string]any{}}
	if data := r.documents[id]; len(data) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&doc); err != nil {
			return config.Document{}, err
		}
	}
	return doc, nil
}

func (r *Repository) Save(ctx context.Context, id string, enabled bool, view config.View) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.SaveError != nil {
		return r.SaveError
	}
	data, err := json.Marshal(config.Document{Version: config.CurrentVersion, Enabled: enabled, Values: view.Values()})
	if err != nil {
		return err
	}
	if r.documents == nil {
		r.documents = map[string][]byte{}
	}
	r.documents[id] = data
	return nil
}

func (r *Repository) Revision(ctx context.Context, id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(r.documents[id])), nil
}
