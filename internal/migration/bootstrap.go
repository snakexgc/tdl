package migration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snakexgc/tdl/interfaces/types"
	legacy "github.com/snakexgc/tdl/pkg/config"
)

// ComponentDirectory isolates account settings without using account names as paths.
func ComponentDirectory(home, account string) string {
	if account == "" {
		account = string(types.DefaultAccount)
	}
	return filepath.Join(home, "components", base64.RawURLEncoding.EncodeToString([]byte(account)))
}

// EnsureComponents imports the compatibility file once, without overwriting it
// or existing component settings. The completion marker guards crash recovery.
func EnsureComponents(ctx context.Context, home string, cfg *legacy.Config) (string, error) {
	destination := ComponentDirectory(home, cfg.Namespace)
	if stat, err := os.Stat(destination); err == nil {
		if !stat.IsDir() {
			return "", fmt.Errorf("component path is not a directory: %s", destination)
		}
		data, err := os.ReadFile(filepath.Join(destination, "migration.json"))
		if err != nil {
			return "", fmt.Errorf("component import is incomplete at %s: %w", destination, err)
		}
		var report Plan
		if err := json.Unmarshal(data, &report); err != nil {
			return "", err
		}
		expected := cfg.Namespace
		if expected == "" {
			expected = string(types.DefaultAccount)
		}
		if string(report.Account) != expected {
			return "", fmt.Errorf("component import account mismatch")
		}
		return destination, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	plan, err := Prepare(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if plan.Account == "" {
		plan.Account = types.DefaultAccount
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return "", err
	}
	if err := plan.Write(ctx, destination); err != nil {
		return "", err
	}
	return destination, nil
}
