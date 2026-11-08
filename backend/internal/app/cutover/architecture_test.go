package cutover_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCompositionBoundary(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	concrete := []string{"/repository/postgres/", "/adapters/codex", "/delivery/"}
	clients := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("..", entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range concrete {
				if strings.Contains(path, marker) && entry.Name() != "app.go" && entry.Name() != "codex_clients.go" {
					t.Errorf("%s imports concrete package %s; only app.go and codex_clients.go may wire it", entry.Name(), path)
				}
			}
		}
		if entry.Name() == "app.go" {
			ast.Inspect(file, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
						if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == "codexadapter" && selector.Sel.Name == "NewClient" {
							clients++
						}
					}
				}
				return true
			})
		}
	}
	// The open core registers the chat's bundle; the helpers' bundles come with the build (DA16.4).
	if !registersBundle(t, filepath.Join("..", "app.go"), "researchchat") {
		t.Error("app.go does not register the researchchat bundle")
	}
	// One client per Codex flow, started by one call site over the chat and the flows of the build (specs/2026-11-02-image-workshop.md IW4.1).
	if clients != 1 {
		t.Errorf("app.go constructs Codex clients at %d call sites, want exactly one", clients)
	}
}

func TestJobWorkerRunnerHasNoOuterRestartLoop(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "job_workers.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var target *ast.FuncDecl
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "startJobWorkerRunner" {
			target = function
			break
		}
	}
	if target == nil {
		t.Fatal("startJobWorkerRunner not found")
	}
	runs := 0
	ast.Inspect(target.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.ForStmt); ok {
			t.Error("startJobWorkerRunner must not restart jobworker.Run")
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Run" {
				runs++
			}
		}
		return true
	})
	if runs != 1 {
		t.Errorf("startJobWorkerRunner calls Run %d times, want exactly once", runs)
	}
}

// registersBundle says the file calls Bundle of the Codex delivery package of the bundle.
func registersBundle(t *testing.T, path, bundle string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	alias := ""
	for _, imported := range file.Imports {
		importPath, _ := strconv.Unquote(imported.Path.Value)
		if strings.HasSuffix(importPath, "/delivery/codex/"+bundle) {
			alias = bundle
			if imported.Name != nil {
				alias = imported.Name.Name
			}
		}
	}
	registered := false
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if identifier, ok := selector.X.(*ast.Ident); ok && alias != "" && identifier.Name == alias && selector.Sel.Name == "Bundle" {
					registered = true
				}
			}
		}
		return true
	})
	return registered
}
