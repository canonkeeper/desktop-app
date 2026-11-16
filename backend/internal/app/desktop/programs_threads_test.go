// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// lockedForGood names the functions of the Go code under roots, tests and testdata aside, that lock their goroutine to its
// thread (runtime.LockOSThread) and never unlock it in the same function: such a goroutine ends its thread when it
// returns, and the kernel then kills every program that thread started with the signal of the death of its parent.
func lockedForGood(t *testing.T, roots ...string) []string {
	t.Helper()
	var found []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files := token.NewFileSet()
			file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			runtimeName := ""
			for _, spec := range file.Imports {
				if imported, _ := strconv.Unquote(spec.Path.Value); imported == "runtime" {
					runtimeName = "runtime"
					if spec.Name != nil {
						runtimeName = spec.Name.Name
					}
				}
			}
			if runtimeName == "" {
				return nil
			}
			ast.Inspect(file, func(node ast.Node) bool {
				var body *ast.BlockStmt
				switch function := node.(type) {
				case *ast.FuncDecl:
					body = function.Body
				case *ast.FuncLit:
					body = function.Body
				default:
					return true
				}
				if body != nil && callsRuntime(body, runtimeName, "LockOSThread") && !callsRuntime(body, runtimeName, "UnlockOSThread") {
					found = append(found, files.Position(node.Pos()).String())
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return found
}

// callsRuntime says whether body itself — its function literals aside, which are functions of their own — calls the
// function name of the package runtime, imported as runtimeName.
func callsRuntime(body *ast.BlockStmt, runtimeName, name string) bool {
	called := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if selector, ok := node.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == name {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == runtimeName {
					called = true
				}
			}
		}
		return !called
	})
	return called
}

// specs/2026-11-16-desk-restore-orphans.md RO1.1: the signal of the death of the parent that ends a program of PostgreSQL
// with the backend comes when the thread that started the program ends (go.dev/issue/27505), and Go ends a thread only
// with a goroutine that returns locked to it. No goroutine of the backend does: no function of its code locks its thread
// without unlocking it.
func TestNoGoroutineOfTheBackendEndsLockedToItsThread(t *testing.T) {
	backend := filepath.Join("..", "..", "..")
	if found := lockedForGood(t, filepath.Join(backend, "internal"), filepath.Join(backend, "cmd")); len(found) > 0 {
		t.Fatalf("these functions lock their thread and never unlock it — a program of PostgreSQL started on it dies amid its work:\n%s",
			strings.Join(found, "\n"))
	}
}

// The guard of the threads names a function that locks for good, in a declaration or a literal, under any name of the
// package runtime, and leaves alone a function that unlocks, a test and testdata.
func TestTheGuardOfTheThreadsCatchesALockNeverUndone(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, source)
	}
	write("held.go", "package p\n\nimport \"runtime\"\n\nfunc held() {\n\truntime.LockOSThread()\n\tdefer runtime.UnlockOSThread()\n}\n")
	write("lost.go", "package p\n\nimport rt \"runtime\"\n\nfunc lost() {\n\tgo func() {\n\t\trt.LockOSThread()\n\t}()\n}\n")
	write("forever.go", "package p\n\nimport \"runtime\"\n\nfunc forever() {\n\truntime.LockOSThread()\n\tselect {}\n}\n")
	write("lost_test.go", "package p\n\nimport \"runtime\"\n\nfunc init() { runtime.LockOSThread() }\n")
	write(filepath.Join("testdata", "lost.go"), "package p\n\nimport \"runtime\"\n\nfunc init() { runtime.LockOSThread() }\n")
	found := lockedForGood(t, root)
	for index, place := range found {
		found[index] = filepath.Base(strings.Split(place, ":")[0])
	}
	slices.Sort(found)
	if !slices.Equal(found, []string{"forever.go", "lost.go"}) {
		t.Fatalf("the guard named %v", found)
	}
}
