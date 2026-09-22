package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestTaskKeyOwnershipAndBSWDirection(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, layer := range []string{adapterLayer, applicationLayer, "bsw", runtimeLayer} {
		err := filepath.WalkDir(filepath.Join(root, layer), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if layer == "bsw" {
				for _, spec := range source.Imports {
					target, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						return err
					}
					if forbiddenBSWImport(target) {
						t.Errorf("%s imports upper layer %s", rel, target)
					}
				}
			}
			if !strings.HasPrefix(rel, "bsw/cdd/taskhub/") {
				ast.Inspect(source, func(node ast.Node) bool {
					literal, ok := node.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						return true
					}
					for _, prefix := range []string{"watch.download.", "watch.aria2.", "download.local.", "forward.job.", "forward.index"} {
						if strings.HasPrefix(value, prefix) {
							t.Errorf("%s duplicates taskhub storage key %q", rel, value)
						}
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func forbiddenBSWImport(target string) bool {
	for _, layer := range []string{adapterLayer, applicationLayer, runtimeLayer} {
		prefix := "github.com/snakexgc/tdl/" + layer
		if target == prefix || strings.HasPrefix(target, prefix+"/") {
			return true
		}
	}
	return false
}

func TestBSWImportBoundary(t *testing.T) {
	const module = "github.com/snakexgc/tdl/"
	for _, target := range []string{adapterLayer, "app/runtime", applicationLayer, "application/filter.rules", runtimeLayer, "rte/config"} {
		if !forbiddenBSWImport(module + target) {
			t.Errorf("BSW boundary permits upper layer %s", target)
		}
	}
	for _, target := range []string{"interfaces/ports", "bsw/services/nvm", "pkg/kv"} {
		if forbiddenBSWImport(module + target) {
			t.Errorf("BSW boundary rejects lower layer %s", target)
		}
	}
}
