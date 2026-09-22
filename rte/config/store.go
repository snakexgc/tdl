package config

import "context"

type Document struct {
	Enabled bool           `json:"enabled"`
	Values  map[string]any `json:"values"`
}

// Repository is the persistence boundary implemented by configuration.manager.
type Repository interface {
	Load(context.Context, string) (Document, error)
	Save(context.Context, string, bool, View) error
	Revision(context.Context, string) (string, error)
}

type Store struct{ repository Repository }

func NewManaged(repository Repository) *Store { return &Store{repository: repository} }

func (s *Store) Load(ctx context.Context, id string) (Document, error) {
	return s.repository.Load(ctx, id)
}

func (s *Store) Save(ctx context.Context, id string, enabled bool, view View) error {
	return s.repository.Save(ctx, id, enabled, view)
}

func (s *Store) Revision(ctx context.Context, id string) (string, error) {
	return s.repository.Revision(ctx, id)
}
