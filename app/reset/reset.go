// Package reset clears application state only after all services and files close.
package reset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

const (
	dataDirectory     = ".tdl"
	unifiedConfigFile = "tdl_config.json"
)

var pending atomic.Pointer[Plan]

// Plan is server-owned. HTTP clients cannot select deletion paths.
type Plan struct{ home string }

func New(home string) *Plan { return &Plan{home: home} }
func Request(plan *Plan)    { pending.Store(plan) }
func Requested() *Plan      { return pending.Load() }

// Targets preflights the current storage and configuration before confirmation.
func (p *Plan) Targets() ([]string, error) {
	home, err := p.open()
	if err != nil {
		return nil, err
	}
	defer home.Close()
	return []string{filepath.Join(home.Name(), dataDirectory), filepath.Join(home.Name(), unifiedConfigFile)}, nil
}

// Execute runs after services and files close; the data mount itself is retained.
func (p *Plan) Execute() error {
	home, err := p.open()
	if err != nil {
		return err
	}
	defer home.Close()
	if err := clearDirectory(home, dataDirectory); err != nil {
		return err
	}
	if err := home.Remove(unifiedConfigFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return removeEntries(home, func(name string) bool {
		return strings.HasPrefix(name, ".tdl_config.json.tmp-")
	}, true)
}

func (p *Plan) open() (*os.Root, error) {
	if p == nil || !filepath.IsAbs(p.home) || filepath.Dir(filepath.Clean(p.home)) == filepath.Clean(p.home) {
		return nil, errors.New("invalid application home for reset")
	}
	home, err := os.OpenRoot(p.home)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{dataDirectory, unifiedConfigFile} {
		if err := checkEntry(home, name, name == dataDirectory); err != nil {
			_ = home.Close()
			return nil, err
		}
	}
	return home, nil
}

func checkEntry(root *os.Root, name string, directory bool) error {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return fmt.Errorf("reset target must be a regular %s: %s", map[bool]string{true: "directory", false: "file"}[directory], filepath.Join(root.Name(), name))
	}
	return nil
}

func clearDirectory(root *os.Root, name string) error {
	directory, err := root.OpenRoot(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	return removeEntries(directory, func(string) bool { return true }, false)
}

func removeEntries(root *os.Root, matches func(string) bool, filesOnly bool) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := file.ReadDir(-1)
	_ = file.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if matches(entry.Name()) && (!filesOnly || !entry.IsDir()) {
			if err := root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
