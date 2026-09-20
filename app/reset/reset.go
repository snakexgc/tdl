// Package reset clears application state only after all services and files close.
package reset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
)

const (
	dataDirectory      = ".tdl"
	componentDirectory = "components"
	configFile         = "config.json"
)

var (
	pending       atomic.Pointer[Plan]
	componentFile = regexp.MustCompile(`^swc-[a-z][a-z0-9._-]*\.json$`)
)

// Plan is server-owned. HTTP clients cannot select deletion paths.
type Plan struct{ home, extra string }

func New(home, components string) *Plan { return &Plan{home: home, extra: components} }
func Request(plan *Plan)                { pending.Store(plan) }
func Requested() *Plan                  { return pending.Load() }

// Targets also preflights every root before a reset can be confirmed.
func (p *Plan) Targets() ([]string, error) {
	home, extra, err := p.open()
	if err != nil {
		return nil, err
	}
	defer home.Close()
	targets := []string{filepath.Join(home.Name(), dataDirectory), filepath.Join(home.Name(), configFile), filepath.Join(home.Name(), componentDirectory)}
	if extra != nil {
		defer extra.Close()
		targets = append(targets, extra.Name()+"（仅 swc-*.json、secrets 中的组件密钥及迁移记录）")
	}
	return targets, nil
}

// Execute must be called after runtime shutdown, database close and logger close.
// Keep directories themselves, so bind-mounted data directories work in Docker.
func (p *Plan) Execute() error {
	home, extra, err := p.open()
	if err != nil {
		return err
	}
	defer home.Close()
	if extra != nil {
		defer extra.Close()
		if err := clearComponents(extra); err != nil {
			return err
		}
	}
	for _, name := range []string{dataDirectory, componentDirectory} {
		if err := clearDirectory(home, name); err != nil {
			return err
		}
	}
	if err := home.Remove(configFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := removeEntries(home, func(name string) bool { return strings.HasPrefix(name, ".config.json.tmp-") }, true); err != nil {
		return err
	}
	return nil
}

func (p *Plan) open() (*os.Root, *os.Root, error) {
	if p == nil || !filepath.IsAbs(p.home) || filepath.Dir(filepath.Clean(p.home)) == filepath.Clean(p.home) {
		return nil, nil, errors.New("invalid application home for reset")
	}
	home, err := os.OpenRoot(p.home)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.Root, *os.Root, error) { _ = home.Close(); return nil, nil, err }
	for _, name := range []string{dataDirectory, componentDirectory, configFile} {
		if err := checkEntry(home, name, name != configFile); err != nil {
			return fail(err)
		}
	}
	if p.extra == "" {
		return home, nil, nil
	}
	extraPath, err := filepath.Abs(p.extra)
	if err != nil {
		return fail(err)
	}
	// All default account directories are already covered by components/.
	for _, directory := range []string{componentDirectory, dataDirectory} {
		rel, err := filepath.Rel(filepath.Join(p.home, directory), extraPath)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return home, nil, nil
		}
	}
	info, err := os.Lstat(extraPath)
	if os.IsNotExist(err) {
		return home, nil, nil
	}
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail(errors.New("component configuration directory must not be a link"))
	}
	extra, err := os.OpenRoot(extraPath)
	if err != nil {
		return fail(err)
	}
	if err := checkEntry(extra, "secrets", true); err != nil {
		_ = extra.Close()
		return fail(err)
	}
	return home, extra, nil
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

// Custom configuration directories may contain unrelated files. Remove only
// TDL-owned documents and every immutable secret generation, not the whole root.
func clearComponents(root *os.Root) error {
	if err := removeEntries(root, func(name string) bool {
		return componentFile.MatchString(name) || name == "migration.json" || strings.HasPrefix(name, ".config-")
	}, true); err != nil {
		return err
	}
	secrets, err := root.OpenRoot("secrets")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer secrets.Close()
	return removeEntries(secrets, componentFile.MatchString, true)
}
