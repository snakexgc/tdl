// Package architecture keeps the new component boundary enforceable while
// legacy packages are migrated incrementally.
package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	interfacesLayer  = "interfaces"
	runtimeLayer     = "rte"
	applicationLayer = "application"
	legacyLayer      = "app"
)

func TestComponentDependencyDirection(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	const module = "github.com/snakexgc/tdl/"
	for _, layer := range []string{interfacesLayer, runtimeLayer, applicationLayer} {
		err := filepath.WalkDir(filepath.Join(root, layer), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			for _, spec := range source.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(importPath, module) {
					continue
				}
				target := strings.TrimPrefix(importPath, module)
				allowed := target == interfacesLayer || strings.HasPrefix(target, "interfaces/")
				if layer != interfacesLayer {
					allowed = allowed || target == runtimeLayer || strings.HasPrefix(target, "rte/")
				}
				if layer == runtimeLayer {
					allowed = allowed || strings.HasPrefix(target, "bsw/")
				}
				if rel == applicationLayer {
					allowed = allowed || strings.HasPrefix(target, "application/")
				}
				if strings.HasPrefix(rel, "application/") {
					component := strings.Join(strings.Split(rel, "/")[:2], "/")
					allowed = allowed || target == component || strings.HasPrefix(target, component+"/")
				}
				if !allowed {
					t.Errorf("%s imports forbidden package %s", path, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
