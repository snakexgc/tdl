package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const CurrentVersion = 1

var componentID = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

type Document struct {
	Version int            `json:"version"`
	Enabled bool           `json:"enabled"`
	Values  map[string]any `json:"values"`
	Secrets string         `json:"secrets,omitempty"`
}

// Store contains component-owned configuration files for one runtime scope.
// Construction is side-effect free. Save replaces one complete document; it
// does not promise a transaction across multiple component files.
type Store struct{ directory string }

func NewStore(directory string) *Store { return &Store{directory: directory} }

// Revision hashes the public document, including its immutable secret reference,
// without exposing secret values. It supports optimistic control-plane edits.
func (s *Store) Revision(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := s.path(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (s *Store) path(id string) (string, error) {
	if s.directory == "" || !componentID.MatchString(id) {
		return "", fmt.Errorf("invalid component configuration path")
	}
	return filepath.Join(s.directory, "swc-"+id+".json"), nil
}

func (s *Store) Load(ctx context.Context, id string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	path, err := s.path(id)
	if err != nil {
		return Document{}, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return Document{Version: CurrentVersion, Enabled: true, Values: map[string]any{}}, nil
	}
	if err != nil {
		return Document{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Document{}, err
	}
	if info.Size() > 1<<20 {
		return Document{}, fmt.Errorf("component configuration exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Document{}, fmt.Errorf("trailing component configuration data")
	}
	if document.Version != CurrentVersion {
		return Document{}, fmt.Errorf("unsupported component configuration version %d", document.Version)
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if document.Secrets != "" {
		name := document.Secrets
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "swc-"+id+"-") || !strings.HasSuffix(name, ".json") {
			return Document{}, fmt.Errorf("invalid secret reference")
		}
		secrets, err := loadSecrets(ctx, filepath.Join(s.directory, "secrets", name))
		if err != nil {
			return Document{}, fmt.Errorf("load component secrets: %w", err)
		}
		if document.Values == nil {
			document.Values = make(map[string]any)
		}
		for key, value := range secrets {
			if _, exists := document.Values[key]; exists {
				return Document{}, fmt.Errorf("duplicate secret field %q", key)
			}
			document.Values[key] = value
		}
	}
	return document, nil
}

// Save syncs the temporary file before replacing the previous document. A
// validation/IO/cancellation failure before rename leaves the old file intact.
func (s *Store) Save(ctx context.Context, id string, enabled bool, view View) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(id)
	if err != nil {
		return err
	}
	values := make(map[string]any, len(view.values))
	secrets := make(map[string]json.RawMessage)
	for name, value := range view.values {
		if view.secrets[name] {
			secrets[name] = value
		} else {
			values[name] = value
		}
	}
	secretName := ""
	if len(secrets) > 0 {
		var err error
		secretName, err = s.saveSecrets(ctx, id, secrets)
		if err != nil {
			return err
		}
	}
	published := false
	defer func() {
		if !published && secretName != "" {
			_ = os.Remove(filepath.Join(s.directory, "secrets", secretName))
		}
	}()
	data, err := json.MarshalIndent(Document{Version: CurrentVersion, Enabled: enabled, Values: values, Secrets: secretName}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("component configuration exceeds size limit")
	}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.directory, ".config-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	published = true
	return nil
}

// Secret generations are immutable. The public document rename is the commit
// point; older generations remain available for concurrent readers and backups.
func (s *Store) saveSecrets(ctx context.Context, id string, values map[string]json.RawMessage) (string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", fmt.Errorf("component secrets exceed size limit")
	}
	directory := filepath.Join(s.directory, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, "swc-"+id+"-*.json")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	keep = true
	return filepath.Base(file.Name()), nil
}

func loadSecrets(ctx context.Context, path string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("component secrets exceed size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing secret data")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
