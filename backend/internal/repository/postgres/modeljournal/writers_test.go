// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	domainPath     = "canonkeeper/backend/internal/domain/modeljournal"
	repositoryPath = "canonkeeper/backend/internal/repository/postgres/modeljournal"
)

// journalTables are the tables of the journal of what went out and their neighbours by name (migration 0155): the records,
// what a thread named and has not sent, the helpers of a thread, the rows of the requests.
const journalTables = `(public\.)?(model_disclosures\w*|model_requests|model_thread_recipients)`

// writes are the statements that change the rows of the journal; tableName is a string that is the name of one of its
// tables alone — a name for pgx.Identifier, CopyFrom or a statement built at run time.
var (
	writes    = regexp.MustCompile(`(?i)\b(insert\s+into|update|delete\s+from|truncate(\s+table)?|merge\s+into|copy)\s+` + journalTables + `\b`)
	tableName = regexp.MustCompile(`^"?` + journalTables + `"?$`)
)

// writer is a place of the tree that writes the journal or builds a sending for it.
type writer struct{ path, what string }

// records are the values of the domain of the journal that are the one way to the statement of a record: a sending of the
// gateway and a reading of a program through the bridge (specs/2026-11-15-desk-mcp.md MC4.2).
var records = map[string]bool{"Sending": true, "ProgramRead": true}

// writersOf are the places among the files that write the rows of the journal — a statement in a string or a constant
// folded from strings, the name of a table alone — or name the value of a record (modeljournal.Sending or
// modeljournal.ProgramRead in any form: a literal, a variable, new, a parameter), which is the one way to the statement of a
// record; a dot import of the domain of the journal hides it, so it is a writer itself.
func writersOf(t *testing.T, root string, files []string) []writer {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed := map[string]*ast.File{}
	constants := map[string]map[string]string{}
	for _, file := range files {
		tree, err := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		parsed[file] = tree
		directory := filepath.Dir(file)
		if constants[directory] == nil {
			constants[directory] = map[string]string{}
		}
		for _, declaration := range tree.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for index, name := range value.Names {
					if index < len(value.Values) {
						if text, ok := folded(value.Values[index], nil); ok {
							constants[directory][name.Name] = text
						}
					}
				}
			}
		}
	}
	var found []writer
	for _, file := range files {
		tree := parsed[file]
		relative, _ := filepath.Rel(root, file)
		relative = filepath.ToSlash(relative)
		domainName := ""
		for _, spec := range tree.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == domainPath {
				domainName = "modeljournal"
				if spec.Name != nil {
					domainName = spec.Name.Name
				}
				if domainName == "." {
					found = append(found, writer{relative, "dot import"})
				}
			}
		}
		known := constants[filepath.Dir(file)]
		ast.Inspect(tree, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BasicLit, *ast.BinaryExpr, *ast.ParenExpr:
				text, ok := folded(node.(ast.Expr), known)
				if !ok {
					return true
				}
				if match := writes.FindStringSubmatch(text); match != nil {
					found = append(found, writer{relative, strings.ToUpper(strings.Join(strings.Fields(match[1]), " "))})
				} else if tableName.MatchString(strings.TrimSpace(text)) {
					found = append(found, writer{relative, "table name"})
				}
				return false
			case *ast.SelectorExpr:
				if ident, ok := node.X.(*ast.Ident); ok && domainName != "" && ident.Name == domainName && records[node.Sel.Name] {
					found = append(found, writer{relative, node.Sel.Name})
				}
			}
			return true
		})
	}
	return dedupe(found)
}

// folded is the value of a constant expression of strings: literals joined by +, constants of the package by their names.
func folded(expression ast.Expr, known map[string]string) (string, bool) {
	switch expression := expression.(type) {
	case *ast.BasicLit:
		if expression.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(expression.Value)
		return text, err == nil
	case *ast.ParenExpr:
		return folded(expression.X, known)
	case *ast.Ident:
		text, ok := known[expression.Name]
		return text, ok
	case *ast.BinaryExpr:
		if expression.Op != token.ADD {
			return "", false
		}
		left, ok := folded(expression.X, known)
		if !ok {
			return "", false
		}
		right, ok := folded(expression.Y, known)
		return left + right, ok
	}
	return "", false
}

func dedupe(found []writer) []writer {
	var unique []writer
	for _, place := range found {
		if !slices.Contains(unique, place) {
			unique = append(unique, place)
		}
	}
	return unique
}

// listedPackage is a package as go list names it.
type listedPackage struct {
	ImportPath string
	Dir        string
	Export     string
	GoFiles    []string
	Imports    []string
	Standard   bool
}

// listed are the packages of the backend and their dependencies with their export data, by import path.
func listed(t *testing.T, root string) map[string]listedPackage {
	t.Helper()
	command := exec.Command("go", "list", "-e", "-export", "-deps", "-json", "./...")
	command.Dir = root
	var out, failure bytes.Buffer
	command.Stdout, command.Stderr = &out, &failure
	if err := command.Run(); err != nil {
		t.Fatalf("go list: %v %s", err, failure.String())
	}
	packages := map[string]listedPackage{}
	decoder := json.NewDecoder(&out)
	for {
		var one listedPackage
		if err := decoder.Decode(&one); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		packages[one.ImportPath] = one
	}
	return packages
}

// typed is a package type-checked against the export data of its dependencies.
func typed(t *testing.T, packages map[string]listedPackage, fileSet *token.FileSet, importPath string, files []*ast.File) (*types.Info, error) {
	t.Helper()
	lookup := func(path string) (io.ReadCloser, error) {
		known, ok := packages[path]
		if !ok || known.Export == "" {
			return nil, errors.New("no export data of " + path)
		}
		return os.Open(known.Export)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	config := types.Config{Importer: importer.ForCompiler(fileSet, "gc", lookup), Error: func(error) {}}
	_, err := config.Check(importPath, fileSet, files, info)
	return info, err
}

// writerCalls are the calls that write the journal: the methods of the store that write it, the statement of a row, and any
// function or method that takes a record.
var writerCalls = map[string]bool{"Send": true, "RecordRead": true, "Record": true, "Name": true, "Withdraw": true, "Clear": true, "Insert": true}

// typedWritersOf are the places of a type-checked package that hold a value of modeljournal.Sending or
// modeljournal.ProgramRead, however it was made, call a writer of the journal, or declare a function or a method that takes a
// record — an implementation of a port of the journal such as programreads.Journal (specs/2026-11-14-desk-journal.md JR1.1,
// specs/2026-11-15-desk-mcp.md MC4.2).
func typedWritersOf(root string, fileSet *token.FileSet, files map[string]*ast.File, info *types.Info) []writer {
	recordOf := func(kind types.Type) string {
		if pointer, ok := kind.(*types.Pointer); ok {
			kind = pointer.Elem()
		}
		named, ok := kind.(*types.Named)
		if !ok || !records[named.Obj().Name()] || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != domainPath {
			return ""
		}
		return named.Obj().Name()
	}
	var found []writer
	for path, file := range files {
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		takesOf := func(signature *types.Signature) bool {
			for index := range signature.Params().Len() {
				if recordOf(signature.Params().At(index).Type()) != "" {
					return true
				}
			}
			return false
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if declaration, ok := node.(*ast.FuncDecl); ok {
				if function, ok := info.Defs[declaration.Name].(*types.Func); ok && takesOf(function.Type().(*types.Signature)) {
					found = append(found, writer{relative, "takes " + function.Name()})
				}
				return true
			}
			switch node := node.(type) {
			case ast.Expr:
				if literal, ok := node.(*ast.FuncLit); ok {
					if signature, ok := info.Types[literal].Type.(*types.Signature); ok && takesOf(signature) {
						found = append(found, writer{relative, "takes a function literal"})
					}
				}
				if value, ok := info.Types[node]; ok && value.Type != nil && !value.IsType() && recordOf(value.Type) != "" {
					found = append(found, writer{relative, recordOf(value.Type)})
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name *ast.Ident
				switch callee := call.Fun.(type) {
				case *ast.SelectorExpr:
					name = callee.Sel
				case *ast.Ident:
					name = callee
				}
				function, ok := info.Uses[name].(*types.Func)
				if !ok {
					return true
				}
				takes := takesOf(function.Type().(*types.Signature))
				ofTheStore := function.Pkg() != nil && function.Pkg().Path() == repositoryPath && writerCalls[function.Name()]
				if takes || ofTheStore {
					found = append(found, writer{relative, "call " + function.Name()})
				}
			}
			return true
		})
	}
	return dedupe(found)
}

// scanned says the types of a package are read: every package of the backend but the domain of the journal, which only defines
// the records — a package that sees a port of the journal and not the journal (the door of the bridge sees
// programreads.Journal) may not write it either.
func scanned(known listedPackage) bool {
	return !known.Standard && strings.HasPrefix(known.ImportPath, "canonkeeper/backend/") && known.ImportPath != domainPath && len(known.GoFiles) > 0
}

// sourceFiles are the non-test Go files of the backend.
func sourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "testdata" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// allowedWriters are the only writers of the journal (specs/2026-11-14-desk-journal.md JR1.1, JR2.6;
// specs/2026-11-15-desk-mcp.md MC4.2): the gateway builds every sending through its port and writes the rows of its requests;
// the readings of the programs through the bridge build every reading of a program and hand it to their port
// (programreads.Journal: only services/programreads calls its method, only the store implements it); the store writes a record
// of either, what a thread named by its statements, a row of a request by one statement, which the ledger of the
// consent calls for a request of the reading in the transaction of its count, and deletes the rows by the clearing; the chat
// names what it gives through its port, which only the store implements; the inventory of the deletion names the tables.
var allowedWriters = []writer{
	{"internal/adapters/llmgateway/gateway.go", "Sending"},
	{"internal/adapters/llmgateway/account.go", "Sending"},
	{"internal/adapters/llmgateway/account.go", "call write"},
	{"internal/adapters/llmgateway/responses.go", "Sending"},
	{"internal/adapters/llmgateway/responses.go", "call write"},
	{"internal/adapters/llmgateway/responses.go", "call Send"},
	{"internal/adapters/llmgateway/responses.go", "takes write"},
	{"internal/services/programreads/reads.go", "ProgramRead"},
	{"internal/services/programreads/reads.go", "call RecordRead"},
	{"internal/repository/postgres/modeljournal/reads.go", "ProgramRead"},
	{"internal/repository/postgres/modeljournal/reads.go", "INSERT INTO"},
	{"internal/repository/postgres/modeljournal/reads.go", "takes RecordRead"},
	{"internal/repository/postgres/modeljournal/sendings.go", "Sending"},
	{"internal/repository/postgres/modeljournal/sendings.go", "INSERT INTO"},
	{"internal/repository/postgres/modeljournal/sendings.go", "takes Send"},
	{"internal/repository/postgres/modeljournal/sendings.go", "DELETE FROM"},
	{"internal/repository/postgres/modeljournal/journal.go", "INSERT INTO"},
	{"internal/repository/postgres/modeljournal/journal.go", "call Insert"},
	{"internal/repository/postgres/modeljournal/days.go", "DELETE FROM"},
	{"internal/repository/postgres/readingconsent/ledger.go", "call Insert"},
	{"internal/domain/deletion/table_inventory.go", "table name"},
}

// JR1.1, MC4.2 (the invariant «the journal is written by the gateway and the door of the bridge»): in the backend only the
// gateway builds a sending, only the readings of the programs build a reading of a program, and only the store of the journal
// writes its rows — the statements of a record, of what a thread named, of a row of a request, and the clearing; no UPDATE.
// The places are read in the text of every file and in the types of every package that imports the journal: a value of a
// record made in any way, a call of a writer. A third writer in any of the forms the checks named fails the guard.
func TestTheGatewayAndTheDoorOfTheBridgeWriteTheJournal(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found := writersOf(t, root, sourceFiles(t, root))
	packages := listed(t, root)
	for _, known := range packages {
		if !scanned(known) {
			continue
		}
		fileSet := token.NewFileSet()
		files := map[string]*ast.File{}
		var list []*ast.File
		for _, name := range known.GoFiles {
			path := filepath.Join(known.Dir, name)
			tree, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files[path] = tree
			list = append(list, tree)
		}
		info, err := typed(t, packages, fileSet, known.ImportPath, list)
		if err != nil {
			t.Fatalf("%s: %v", known.ImportPath, err)
		}
		found = append(found, typedWritersOf(root, fileSet, files, info)...)
	}
	found = dedupe(found)
	for _, place := range found {
		if !slices.Contains(allowedWriters, place) {
			t.Errorf("a writer of the journal outside the gateway and its store: %s (%s)", place.path, place.what)
		}
	}
	for _, place := range allowedWriters {
		if !slices.Contains(found, place) {
			t.Errorf("the writer %s (%s) is gone: take it out of the list", place.path, place.what)
		}
	}

	// The forms of a second writer the second check tried (review-journal-gateway, finding 6): each is seen.
	directory := t.TempDir()
	forms := map[string]struct{ source, what string }{
		"literal.go":  {"package second\n\nconst record = `INSERT INTO public.model_disclosures (id) VALUES ($1)`\n", "INSERT INTO"},
		"concat.go":   {"package second\n\nconst head = \"INSERT INTO \"\n\nvar q = head + \"model_disclosures (id) VALUES ($1)\"\n", "INSERT INTO"},
		"requests.go": {"package second\n\nconst q = `INSERT INTO model_requests (id) VALUES ($1)`\n", "INSERT INTO"},
		"pending.go":  {"package second\n\nconst q = `INSERT INTO model_disclosures_pending (id) VALUES ($1)`\n", "INSERT INTO"},
		"copy.go":     {"package second\n\nimport \"github.com/jackc/pgx/v5\"\n\nvar table = pgx.Identifier{\"model_disclosures\"}\n", "table name"},
		"var.go": {"package second\n\nimport \"" + domainPath + "\"\n\nfunc f(journal interface{ Send(any, modeljournal.Sending) error }) {\n\tvar sending modeljournal.Sending\n\t_ = journal.Send(nil, sending)\n}\n",
			"Sending"},
		"new.go": {"package second\n\nimport journal \"" + domainPath + "\"\n\nvar sending = new(journal.Sending)\n", "Sending"},
		"dot.go": {"package second\n\nimport . \"" + domainPath + "\"\n\nvar sending = Sending{}\n", "dot import"},
		// A third writer of a reading of a program (specs/2026-11-15-desk-mcp.md MC4.2): a literal, new.
		"read.go":    {"package second\n\nimport \"" + domainPath + "\"\n\nvar read = modeljournal.ProgramRead{Client: \"x\"}\n", "ProgramRead"},
		"newread.go": {"package second\n\nimport journal \"" + domainPath + "\"\n\nvar read = new(journal.ProgramRead)\n", "ProgramRead"},
	}
	for name, form := range forms {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(form.source), 0o600); err != nil {
			t.Fatal(err)
		}
		if seen := writersOf(t, directory, []string{path}); !slices.Contains(seen, writer{name, form.what}) {
			t.Errorf("a second writer %s went unseen: %v", name, seen)
		}
	}
	// In the types: a sending that comes from a function and a call that hands it on — no name of the type in the text.
	hidden := "package second\n\nimport \"" + domainPath + "\"\n\nfunc made() modeljournal.Sending { return modeljournal.Sending{} }\n\n" +
		"func hand(journal interface{ Send(any, modeljournal.Sending) error }) { _ = journal.Send(nil, made()) }\n"
	fileSet := token.NewFileSet()
	path := filepath.Join(directory, "hidden.go")
	tree, err := parser.ParseFile(fileSet, path, hidden, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := typed(t, packages, fileSet, "canonkeeper/backend/internal/second", []*ast.File{tree})
	if err != nil {
		t.Fatal(err)
	}
	if seen := typedWritersOf(directory, fileSet, map[string]*ast.File{path: tree}, info); !slices.Contains(seen, writer{"hidden.go", "Sending"}) ||
		!slices.Contains(seen, writer{"hidden.go", "call Send"}) {
		t.Fatalf("a sending handed on in the types went unseen: %v", seen)
	}
	// A reading of a program that comes from a function and is handed on by a call in another package: no name of the type.
	reading := "package second\n\nimport \"" + domainPath + "\"\n\nfunc made() modeljournal.ProgramRead { return modeljournal.ProgramRead{} }\n\n" +
		"func hand(journal interface{ RecordRead(any, modeljournal.ProgramRead) error }) { _ = journal.RecordRead(nil, made()) }\n"
	readPath := filepath.Join(directory, "reading.go")
	readTree, err := parser.ParseFile(fileSet, readPath, reading, 0)
	if err != nil {
		t.Fatal(err)
	}
	readInfo, err := typed(t, packages, fileSet, "canonkeeper/backend/internal/third", []*ast.File{readTree})
	if err != nil {
		t.Fatal(err)
	}
	if seen := typedWritersOf(directory, fileSet, map[string]*ast.File{readPath: readTree}, readInfo); !slices.Contains(seen, writer{"reading.go", "ProgramRead"}) ||
		!slices.Contains(seen, writer{"reading.go", "call RecordRead"}) {
		t.Fatalf("a reading of a program handed on in the types went unseen: %v", seen)
	}
	// The forms of the check of the bridge (review-mcp-journal, J-4): the door — a package that sees only the port
	// programreads.Journal — calls its method with a reading made with no name of the type; a second implementation of the port.
	for _, path := range []string{"canonkeeper/backend/internal/delivery/mcp/door", "canonkeeper/backend/internal/services/programreads"} {
		if !scanned(packages[path]) {
			t.Errorf("the types of %s are not read", path)
		}
	}
	for name, form := range map[string]struct{ source, what string }{
		"door.go": {"package door\n\nimport (\n\t\"context\"\n\n\t\"canonkeeper/backend/internal/services/programreads\"\n)\n\n" +
			"func zeroOf[T any](func(context.Context, T) error) T { var zero T; return zero }\n\n" +
			"func hand(ctx context.Context, journal programreads.Journal) { _ = journal.RecordRead(ctx, zeroOf(journal.RecordRead)) }\n", "call RecordRead"},
		"second.go": {"package second\n\nimport (\n\t\"context\"\n\n\t\"" + domainPath + "\"\n)\n\n" +
			"type journal struct{}\n\nfunc (journal) RecordRead(context.Context, modeljournal.ProgramRead) error { return nil }\n", "takes RecordRead"},
	} {
		formSet := token.NewFileSet()
		formPath := filepath.Join(directory, name)
		formTree, err := parser.ParseFile(formSet, formPath, form.source, 0)
		if err != nil {
			t.Fatal(err)
		}
		formInfo, err := typed(t, packages, formSet, "canonkeeper/backend/internal/form/"+strings.TrimSuffix(name, ".go"), []*ast.File{formTree})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if seen := typedWritersOf(directory, formSet, map[string]*ast.File{formPath: formTree}, formInfo); !slices.Contains(seen, writer{name, form.what}) {
			t.Errorf("the form %s went unseen: %v", name, seen)
		}
	}
}
