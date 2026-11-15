// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// roots are the programs of the kit of the app (specs/2026-11-14-desk-journal.md JR5.1): the backend and the program of the
// bridge for the programs of the author (specs/2026-11-15-desk-mcp.md MC4).
var roots = []string{"canonkeeper/backend/cmd/desktop", "canonkeeper/backend/cmd/desktop-mcp"}

// trees are the modules of this tree the closure is read in: the backend and the translation of «Свой ИИ».
var trees = []string{"canonkeeper/backend", "github.com/canonkeeper/desktop-app/translate"}

// reach is a package of the closure that opens a connection or starts a program, and what for; outward — it goes out of the
// computer, so its transport takes the proxy of the environment.
type reach struct {
	purpose string
	outward bool
}

// reaching is the named list of JR5.1: every other package of the closure neither connects nor starts a program.
var reaching = map[string]reach{
	"canonkeeper/backend/internal/adapters/llmgateway":               {"шлюз: провайдер «Своего ИИ»", true},
	"github.com/canonkeeper/desktop-app/translate":                   {"модуль перевода шлюза: провайдер «Своего ИИ»", true},
	"github.com/canonkeeper/desktop-app/translate/internal/provider": {"модуль перевода шлюза: провайдер «Своего ИИ»", true},
	"canonkeeper/backend/internal/adapters/siteapi":                  {"клиент сайта CanonKeeper", true},
	"canonkeeper/backend/internal/adapters/modelapi":                 {"клиент ядра: шлюз на loopback", false},
	"canonkeeper/backend/internal/adapters/codex":                    {"Codex: запуск app-server, канал на stdio", false},
	"canonkeeper/backend/internal/app/desktop":                       {"PostgreSQL: запуск набора, соединение на сокете или 127.0.0.1", false},
	"canonkeeper/backend/internal/store/db":                          {"PostgreSQL: пул соединений на сокете или 127.0.0.1", false},
	// The door of the app listens on a local socket and dials nothing: it stands in no list. That the bridge dials only unix
	// and only the path of mcp.json its own test holds (MC1).
	"canonkeeper/backend/internal/delivery/mcp/bridge": {"мост: локальный сокет из mcp.json", false},
}

// reachers are the names of a package that connect or start a program: a client, a transport, a dial, a lookup of a name, a
// proxy, a mail, a call, a command, a pool — every package of net/* and os/exec of the standard library that does it by a
// name of its own. A package of the table imported with a dot hides its names, so a dot import of it is a breach itself.
var reachers = map[string][]string{
	"net/http": {"Client", "Transport", "DefaultClient", "DefaultTransport", "Get", "Post", "PostForm", "Head"},
	"net": {"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer", "Resolver", "DefaultResolver",
		"LookupAddr", "LookupCNAME", "LookupHost", "LookupIP", "LookupMX", "LookupNS", "LookupPort", "LookupSRV", "LookupTXT"},
	"net/http/httputil":               {"ReverseProxy", "NewSingleHostReverseProxy"},
	"net/http/cgi":                    {"Handler"},
	"net/smtp":                        {"Dial", "SendMail"},
	"net/rpc":                         {"Dial", "DialHTTP", "DialHTTPPath"},
	"net/rpc/jsonrpc":                 {"Dial"},
	"net/textproto":                   {"Dial"},
	"log/syslog":                      {"Dial", "New", "NewLogger"},
	"crypto/tls":                      {"Dial", "DialWithDialer", "Dialer"},
	"os/exec":                         {"Command", "CommandContext", "Cmd"},
	"os":                              {"StartProcess"},
	"syscall":                         {"ForkExec", "StartProcess", "Exec"},
	"github.com/jackc/pgx/v5":         {"Connect", "ConnectConfig"},
	"github.com/jackc/pgx/v5/pgxpool": {"New", "NewWithConfig"},
	"github.com/jackc/pgx/v5/pgconn":  {"Connect", "ConnectConfig"},
}

// named is the default name of an imported package: the last element of its path, without a major version.
func named(importPath string) string {
	name := path.Base(importPath)
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = path.Base(path.Dir(importPath))
	}
	return name
}

// reachersOf are the names that reach in an import: the table, and Dial of every websocket package.
func reachersOf(importPath string) []string {
	if path.Base(importPath) == "websocket" {
		return []string{"Dial", "DialContext", "Dialer"}
	}
	return reachers[importPath]
}

type listed struct {
	ImportPath     string
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	Module         *struct{ Path string }
}

// closure is every package of this tree the roots run on Linux and on Windows, with its non-test files of every system;
// overlay is the overlay of the go command, "" without one.
func closure(t *testing.T, overlay string) map[string][]string {
	t.Helper()
	packages := map[string][]string{}
	for _, system := range []string{"linux", "windows"} {
		args := []string{"list", "-deps", "-json"}
		if overlay != "" {
			args = append(args, "-overlay", overlay)
		}
		list := exec.Command("go", append(args, roots...)...)
		list.Env = append(os.Environ(), "GOOS="+system, "CGO_ENABLED=0")
		var failure bytes.Buffer
		list.Stderr = &failure
		output, err := list.Output()
		if err != nil {
			t.Fatalf("go list for %s: %v %s", system, err, failure.String())
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var pkg listed
			if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			if pkg.Module == nil || !slices.Contains(trees, pkg.Module.Path) {
				continue
			}
			for _, name := range slices.Concat(pkg.GoFiles, pkg.CgoFiles, pkg.IgnoredGoFiles) {
				file := filepath.Join(pkg.Dir, name)
				if !strings.HasSuffix(name, "_test.go") && !slices.Contains(packages[pkg.ImportPath], file) {
					packages[pkg.ImportPath] = append(packages[pkg.ImportPath], file)
				}
			}
		}
	}
	return packages
}

// breaches are what the closure breaks: a package outside the named list that connects or starts a program, and a
// transport of an outward package without the proxy of the environment. replaced are the files of the overlay.
func breaches(t *testing.T, packages map[string][]string, replaced map[string]string) []string {
	t.Helper()
	var found []string
	for importPath, files := range packages {
		known, listed := reaching[importPath]
		for _, file := range files {
			source := file
			if other, ok := replaced[file]; ok {
				source = other
			}
			fileSet := token.NewFileSet()
			parsed, err := parser.ParseFile(fileSet, source, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			names := map[string]string{}
			for _, spec := range parsed.Imports {
				imported, _ := strconv.Unquote(spec.Path.Value)
				name := named(imported)
				if spec.Name != nil {
					name = spec.Name.Name
				}
				names[name] = imported
				if name == "." && len(reachersOf(imported)) > 0 {
					found = append(found, fmt.Sprintf("%s: точка-импорт %s (%s)", importPath, imported, fileSet.Position(spec.Pos())))
				}
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.SelectorExpr:
					ident, ok := node.X.(*ast.Ident)
					if !ok || !slices.Contains(reachersOf(names[ident.Name]), node.Sel.Name) || listed {
						return true
					}
					found = append(found, fmt.Sprintf("%s: %s.%s (%s) — вне списка", importPath, ident.Name, node.Sel.Name, fileSet.Position(node.Pos())))
				case *ast.CompositeLit:
					selector, ok := node.Type.(*ast.SelectorExpr)
					if !ok || !known.outward || selector.Sel.Name != "Transport" {
						return true
					}
					if ident, ok := selector.X.(*ast.Ident); !ok || names[ident.Name] != "net/http" {
						return true
					}
					if !proxied(node, names) {
						found = append(found, fmt.Sprintf("%s: транспорт без прокси окружения (%s)", importPath, fileSet.Position(node.Pos())))
					}
				}
				return true
			})
		}
	}
	slices.Sort(found)
	return found
}

// proxied says a literal of http.Transport takes the proxy of the environment.
func proxied(literal *ast.CompositeLit, names map[string]string) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if key, isIdent := field.Key.(*ast.Ident); !ok || !isIdent || key.Name != "Proxy" {
			continue
		}
		value, ok := field.Value.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		ident, ok := value.X.(*ast.Ident)
		return ok && names[ident.Name] == "net/http" && value.Sel.Name == "ProxyFromEnvironment"
	}
	return false
}

// specs/2026-11-14-desk-journal.md JR5.1: in the closure of the kit of the app only the packages of the named list connect or
// start a program, each for its purpose, and the transport of a package that goes out takes the proxy of the environment.
func TestOnlyTheNamedPackagesOfTheAppReachTheNetworkOrStartPrograms(t *testing.T) {
	packages := closure(t, "")
	for importPath := range reaching {
		if _, ok := packages[importPath]; !ok {
			t.Errorf("the package %s of the list is no longer in the closure: take it out of the list", importPath)
		}
	}
	if found := breaches(t, packages, nil); len(found) != 0 {
		t.Fatalf("the closure of the app reaches outside the list:\n%s", strings.Join(found, "\n"))
	}
}

// JR5.1, the mutations in a copy of the tree (an overlay of the go command): a new package of the closure with an http.Client
// outside the list, and the transport of the client of the site without the proxy, fail the guard.
func TestTheGuardOfTheClosureCatchesANewClientAndATransportWithoutTheProxy(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Dir(filepath.Dir(root))
	scratch := t.TempDir()
	write := func(name, text string) string {
		file := filepath.Join(scratch, name)
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	overlay := func(replace map[string]string) string {
		body, err := json.Marshal(map[string]any{"Replace": replace})
		if err != nil {
			t.Fatal(err)
		}
		return write("overlay.json", string(body))
	}

	leak := map[string]string{
		filepath.Join(backend, "internal", "adapters", "leak", "leak.go"): write("leak.go",
			"package leak\n\nimport \"net/http\"\n\n// Client goes where it likes.\nvar Client = &http.Client{}\n"),
		filepath.Join(root, "leak.go"): write("main_leak.go",
			"package main\n\nimport _ \"canonkeeper/backend/internal/adapters/leak\"\n"),
	}
	found := breaches(t, closure(t, overlay(leak)), leak)
	if !slices.ContainsFunc(found, func(line string) bool {
		return strings.HasPrefix(line, "canonkeeper/backend/internal/adapters/leak: http.Client")
	}) {
		t.Fatalf("a new client outside the list went unseen: %v", found)
	}

	// The forms the third check tried (review-journal-core, C3): a dot import of net/http, a mail, a call of RPC, a reverse
	// proxy, a lookup of a name — each is a breach outside the list.
	sneak := map[string]string{
		filepath.Join(backend, "internal", "adapters", "sneak", "sneak.go"): write("sneak.go", "package sneak\n\nimport (\n\t\"net\"\n\t. \"net/http\"\n"+
			"\t\"net/http/httputil\"\n\t\"net/rpc\"\n\t\"net/smtp\"\n)\n\n// Go reaches out.\nfunc Go() {\n\t_ = smtp.SendMail(\"smtp.example.test:25\", nil, \"a@example.test\", nil, nil)\n"+
			"\t_, _ = net.LookupHost(\"example.test\")\n\t_ = &httputil.ReverseProxy{}\n\t_, _ = rpc.Dial(\"tcp\", \"example.test:1\")\n\t_, _ = (&Client{}).Get(\"https://example.test/\")\n}\n"),
		filepath.Join(root, "sneak.go"): write("main_sneak.go", "package main\n\nimport _ \"canonkeeper/backend/internal/adapters/sneak\"\n"),
	}
	found = breaches(t, closure(t, overlay(sneak)), sneak)
	for _, want := range []string{"точка-импорт net/http", "smtp.SendMail", "net.LookupHost", "httputil.ReverseProxy", "rpc.Dial"} {
		if !slices.ContainsFunc(found, func(line string) bool {
			return strings.HasPrefix(line, "canonkeeper/backend/internal/adapters/sneak: ") && strings.Contains(line, want)
		}) {
			t.Errorf("%s went unseen: %v", want, found)
		}
	}

	site := filepath.Join(backend, "internal", "adapters", "siteapi", "client.go")
	source, err := os.ReadFile(site)
	if err != nil {
		t.Fatal(err)
	}
	without := strings.Replace(string(source), "Proxy:                 http.ProxyFromEnvironment,\n", "", 1)
	if without == string(source) {
		t.Fatal("the transport of the client of the site no longer names its proxy as the mutation expects")
	}
	bare := map[string]string{site: write("client.go", without)}
	found = breaches(t, closure(t, overlay(bare)), bare)
	if !slices.ContainsFunc(found, func(line string) bool {
		return strings.HasPrefix(line, "canonkeeper/backend/internal/adapters/siteapi: транспорт без прокси")
	}) {
		t.Fatalf("a transport without the proxy went unseen: %v", found)
	}
}
