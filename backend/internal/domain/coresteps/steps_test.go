// SPDX-License-Identifier: AGPL-3.0-only

package coresteps

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// call is a call of the core with instructions and an input: the step it is and the words it gives, the very constant
// the source names.
type call struct {
	step  Name
	words string
}

// calls are the calls of the core with instructions and an input as the source writes them, in the package of the call,
// with the step each one is and the constant its words are (specs/2026-11-13-desk-proxy.md PX2.1). A call the table does
// not know is a new call of the core without a step: it fails TestEveryCallOfTheCoreIsAStep until it gets one; a step
// whose words are not those of its call fails it too.
var calls = map[string]call{
	"services/sagamemory: writer.instructions.Text":      {Card, sagamemory.CurrentCardInstructions.Text},
	"services/sagamemory: writer.instructions.Pass":      {CardPass, sagamemory.CurrentCardInstructions.Pass},
	"services/sagamemory: domain.NameInstructions":       {Names, sagamemory.NameInstructions},
	"services/sagalayer: domain.MapInstructions":         {LayerBook, sagalayer.MapInstructions},
	"services/sagalayer: domain.ReduceInstructions":      {LayerReduce, sagalayer.ReduceInstructions},
	"services/sagalayer: domain.VerifyInstructions":      {LayerVerify, sagalayer.VerifyInstructions},
	"services/sagalayer: domain.GapInstructions":         {LayerGap, sagalayer.GapInstructions},
	"services/researchchat: sagamemory.ScanInstructions": {Scan, sagamemory.ScanInstructions},
	"services/authormaterials: domain.MapInstructions":   {Materials, authormaterials.MapInstructions},
}

// TestEveryCallOfTheCoreIsAStep reads every package the desktop build runs — the open core — and finds every call of a
// model with instructions and an input (Complete with three arguments, Stream with four), following an argument a
// function hands on to the calls of that function. The steps are exactly those calls: a new call without a step, a step
// without a call, and a step whose words are not the words of its call fail.
func TestEveryCallOfTheCoreIsAStep(t *testing.T) {
	list := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}} {{.Dir}}{{end}}", "canonkeeper/backend/cmd/desktop")
	output, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const internal = "canonkeeper/backend/internal/"
	found := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		path, dir, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(path, internal) || path == internal+"adapters/modelapi" {
			continue
		}
		for _, expression := range modelCalls(t, dir) {
			found[strings.TrimPrefix(path, internal)+": "+expression] = true
		}
	}
	seen := map[Name]bool{}
	for expression := range found {
		call, known := calls[expression]
		if !known {
			t.Errorf("a call of the core without a step: %s", expression)
			continue
		}
		seen[call.step] = true
	}
	for expression, call := range calls {
		if !found[expression] {
			t.Errorf("the step %s names a call the core no longer makes: %s", call.step, expression)
		}
		if step, ok := Named(string(call.step)); !ok || step.Instructions != call.words {
			t.Errorf("the words of the step %s are not those of its call %s", call.step, expression)
		}
	}
	for _, step := range All() {
		if !seen[step.Name] {
			t.Errorf("the step %s is no call of the core", step.Name)
		}
	}
	if len(All()) != len(seen) {
		t.Errorf("steps %d, calls of steps %d", len(All()), len(seen))
	}
}

// modelCalls are the instruction arguments of the calls of a model in the non-test files of one package, as source text.
func modelCalls(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	var parsed []*ast.File
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(fileSet, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, source)
	}
	var expressions []string
	var follow func(argument ast.Expr, inside *ast.FuncDecl, depth int)
	follow = func(argument ast.Expr, inside *ast.FuncDecl, depth int) {
		if index, handed := parameterIndex(argument, inside); handed && depth < 4 {
			for _, file := range parsed {
				eachCall(file, func(call *ast.CallExpr, caller *ast.FuncDecl) {
					if calledName(call) == inside.Name.Name && index < len(call.Args) {
						follow(call.Args[index], caller, depth+1)
					}
				})
			}
			return
		}
		var text bytes.Buffer
		if err := printer.Fprint(&text, fileSet, argument); err != nil {
			t.Fatal(err)
		}
		expressions = append(expressions, text.String())
	}
	for _, file := range parsed {
		eachCall(file, func(call *ast.CallExpr, caller *ast.FuncDecl) {
			name := calledName(call)
			if name == "Complete" && len(call.Args) == 3 || name == "Stream" && len(call.Args) == 4 {
				follow(call.Args[1], caller, 0)
			}
		})
	}
	sort.Strings(expressions)
	return expressions
}

// eachCall walks the calls of a file with the function each one is in.
func eachCall(file *ast.File, visit func(call *ast.CallExpr, caller *ast.FuncDecl)) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				visit(call, function)
			}
			return true
		})
	}
}

// calledName is the name of a called function or method.
func calledName(call *ast.CallExpr) string {
	switch called := call.Fun.(type) {
	case *ast.Ident:
		return called.Name
	case *ast.SelectorExpr:
		return called.Sel.Name
	}
	return ""
}

// parameterIndex says whether the argument is a parameter of the function it is in, and which one.
func parameterIndex(argument ast.Expr, inside *ast.FuncDecl) (int, bool) {
	identifier, ok := argument.(*ast.Ident)
	if !ok || inside == nil {
		return 0, false
	}
	index := 0
	for _, field := range inside.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == identifier.Name {
				return index, true
			}
			index++
		}
		if len(field.Names) == 0 {
			index++
		}
	}
	return 0, false
}

// Every step has its words and their print, the SHA-256 of the words; two steps never share words, and a step is found
// by its name alone.
func TestEveryStepHasWordsOfItsOwn(t *testing.T) {
	prints := map[string]Name{}
	for _, step := range All() {
		if strings.TrimSpace(step.Instructions) == "" {
			t.Fatalf("the step %s has no words", step.Name)
		}
		sum := sha256.Sum256([]byte(step.Instructions))
		if step.Print != hex.EncodeToString(sum[:]) {
			t.Fatalf("the print of %s = %s", step.Name, step.Print)
		}
		if other, taken := prints[step.Print]; taken {
			t.Fatalf("the steps %s and %s share their words", step.Name, other)
		}
		prints[step.Print] = step.Name
		if named, ok := Named(string(step.Name)); !ok || named.Print != step.Print {
			t.Fatalf("Named(%s) = %+v, %v", step.Name, named, ok)
		}
	}
	if _, ok := Named("card_free"); ok {
		t.Fatal("an unknown name found a step")
	}
}

// The prints of the released versions of the app (DT12.4) are a list in the tree: every print names a step of this tree
// and is a SHA-256; the words of this tree are accepted always, the words of a released version only by their print.
func TestTheWordsOfAStepAreThoseOfTheTreeOrOfAReleasedApp(t *testing.T) {
	raw, err := os.ReadFile("released.json")
	if err != nil {
		t.Fatal(err)
	}
	released, err := parseReleased(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range released.Releases {
		for name, print := range release.Prints {
			if _, ok := Named(name); !ok {
				t.Fatalf("release %s names the step %s this tree does not have", release.Version, name)
			}
			if decoded, err := hex.DecodeString(print); err != nil || len(decoded) != sha256.Size {
				t.Fatalf("release %s, step %s: print %q", release.Version, name, print)
			}
		}
	}
	for _, step := range All() {
		if !Accepts(step.Name, step.Instructions) {
			t.Fatalf("the words of the tree of %s are refused", step.Name)
		}
		if Accepts(step.Name, step.Instructions+".") {
			t.Fatalf("other words of %s are accepted", step.Name)
		}
	}
	if scan, _ := Named(string(Scan)); Accepts(Card, scan.Instructions) {
		t.Fatal("the words of another step are accepted")
	}
	old := "Слова карточки выпущенной версии приложения"
	sum := sha256.Sum256([]byte(old))
	list, err := parseReleased([]byte(`{"releases":[{"version":"1.0.0","prints":{"card":"` + hex.EncodeToString(sum[:]) + `"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !list.accepts(Card, old) || list.accepts(CardPass, old) {
		t.Fatal("a print of a released version is not taken for its step alone")
	}
	for _, broken := range []string{
		`{"releases":[{"version":"1.0.0","prints":{"card":"abc"}}]}`,
		`{"releases":[{"version":"1.0.0","prints":{"card_free":"` + hex.EncodeToString(sum[:]) + `"}}]}`,
		`{"releases":[],"more":true}`,
	} {
		if _, err := parseReleased([]byte(broken)); err == nil {
			t.Fatalf("a broken list is read: %s", broken)
		}
	}
}

// The local gateway of the app names the purpose of a call of the core by its words (specs/2026-11-13-desk-gateway.md
// GW1.7): the words of a step of this tree are that step, any other words — another version's or free text — no step.
func TestTheGatewayFindsAStepByItsWords(t *testing.T) {
	for _, step := range All() {
		if found, ok := Of(step.Instructions); !ok || found.Name != step.Name {
			t.Fatalf("Of(the words of %s) = %+v, %v", step.Name, found, ok)
		}
		if _, ok := Of(step.Instructions + " "); ok {
			t.Fatalf("other words of %s found a step", step.Name)
		}
	}
	if _, ok := Of(""); ok {
		t.Fatal("no words found a step")
	}
}

// specs/2026-11-13-desk-account-ai.md AI5.2: every step names the permit it goes under — scan the permit of its turn, the
// materials theirs, the reading of books a permit of reading — so the site and the app read it from one owner.
func TestEveryStepNamesItsPermit(t *testing.T) {
	want := map[Name]Permit{
		Card: PermitReading, CardPass: PermitReading, Names: PermitReading, LayerBook: PermitReading,
		LayerReduce: PermitReading, LayerVerify: PermitReading, LayerGap: PermitReading,
		Scan: PermitChatTurn, Materials: PermitMaterials,
	}
	for _, step := range All() {
		if step.Permit != want[step.Name] || step.Permit == "" {
			t.Fatalf("the permit of %s = %q, want %q", step.Name, step.Permit, want[step.Name])
		}
	}
	if len(want) != len(All()) {
		t.Fatalf("steps %d, permits %d", len(All()), len(want))
	}
}
