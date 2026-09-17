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
	for _, layer := range []string{"app", "application", "bsw", "rte"} {
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
					for _, forbidden := range []string{"app/", "application/"} {
						if strings.HasPrefix(target, "github.com/snakexgc/tdl/"+forbidden) {
							t.Errorf("%s imports business implementation %s", rel, target)
						}
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
					for _, prefix := range []string{"watch.download.", "watch.aria2.", "watch.internal.", "forward.job.", "forward.index"} {
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
