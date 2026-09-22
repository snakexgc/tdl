package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Snapshot freezes the startup configuration. Reconnecting a service must not
// consume edits that have only been saved for the next process start.
func (s *Store) Snapshot(ctx context.Context, ids []string) (*Store, error) {
	if s == nil {
		return nil, fmt.Errorf("configuration store is unavailable")
	}
	values := make(snapshot, len(ids))
	for _, id := range ids {
		doc, err := s.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		values[id] = data
	}
	return NewManaged(values), nil
}

type snapshot map[string][]byte

func (s snapshot) Load(ctx context.Context, id string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	data, ok := s[id]
	if !ok {
		return Document{}, fmt.Errorf("component %s is absent from startup configuration", id)
	}
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err := decoder.Decode(&doc)
	return doc, err
}

func (snapshot) Save(context.Context, string, bool, View) error {
	return fmt.Errorf("startup configuration is read-only; save to the configuration file and restart")
}

func (s snapshot) Revision(ctx context.Context, id string) (string, error) {
	if _, err := s.Load(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(s[id])), nil
}
