// SPDX-License-Identifier: AGPL-3.0-only

// Package desktoptest runs the backend of the desktop app as its shell does, for the process tests of the open app and of
// the site (specs/2026-11-13-desk-account.md, step 2 of the checklist): cmd/desktop of this tree built with a version and
// the address of the site, the control channel on its stdin and stdout, and a shell that answers secret-save and
// secret-delete the way the test chose. It is code of the tests, not of a build: no program imports it.
package desktoptest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ResourcesVariable names the directory of the bundle the process tests run the backend with: postgres/ (our build of
// PostgreSQL 18), codex/codex-app-server of the go.mod version and codex-homes/. Without it the tests are skipped.
const ResourcesVariable = "CANONKEEPER_DESKTOP_RESOURCES"

// Resources is the bundle of the process tests; a test without it is skipped.
func Resources(t testing.TB) string {
	t.Helper()
	resources := os.Getenv(ResourcesVariable)
	if resources == "" {
		t.Skipf("%s is not set", ResourcesVariable)
	}
	return resources
}

// Program is a build of cmd/desktop of this tree: its name, unique among the programs of a test binary; the version of
// the app it carries (-X main.version), as scripts/desktop-backend.mjs builds it; the address of the site it links with
// (-X main.siteURL), none for the address of the build; the unit of the pauses of the repeats of a call of the model
// (-X main.retryPause), none for the client's own; how often the reading of the helper of the account checks its pauses
// (-X main.resumeEvery), none for ten minutes; and the files go build replaces (-overlay).
type Program struct {
	Name        string
	Version     string
	Site        string
	RetryPause  string
	ResumeEvery string
	Overlay     map[string]string
}

// Programs are the programs a test binary built, each once, in a directory of their own.
type Programs struct {
	mu        sync.Mutex
	directory string
	built     map[string]string
	versions  map[string]string
}

// NewPrograms is an empty set of programs.
func NewPrograms() *Programs {
	return &Programs{built: map[string]string{}, versions: map[string]string{}}
}

// Directory is the directory of the programs, made at its first use: a test keeps the files of its overlays there.
func (programs *Programs) Directory(t testing.TB) string {
	t.Helper()
	programs.mu.Lock()
	defer programs.mu.Unlock()
	return programs.directoryLocked(t)
}

func (programs *Programs) directoryLocked(t testing.TB) string {
	t.Helper()
	if programs.directory == "" {
		directory, err := os.MkdirTemp("", "canonkeeper-desktop-programs-")
		if err != nil {
			t.Fatal(err)
		}
		programs.directory = directory
	}
	return programs.directory
}

// Build builds the program once per name and answers its path.
func (programs *Programs) Build(t testing.TB, program Program) string {
	t.Helper()
	programs.mu.Lock()
	defer programs.mu.Unlock()
	if binary, ok := programs.built[program.Name]; ok {
		return binary
	}
	directory := programs.directoryLocked(t)
	binary := filepath.Join(directory, "desktop-"+program.Name)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	args := []string{"build", "-o", binary}
	var flags []string
	if program.Version != "" {
		flags = append(flags, "-X main.version="+program.Version)
	}
	if program.Site != "" {
		flags = append(flags, "-X main.siteURL="+program.Site)
	}
	if program.ResumeEvery != "" {
		flags = append(flags, "-X main.resumeEvery="+program.ResumeEvery)
	}
	if program.RetryPause != "" {
		flags = append(flags, "-X main.retryPause="+program.RetryPause)
	}
	if len(flags) > 0 {
		args = append(args, "-ldflags="+strings.Join(flags, " "))
	}
	if len(program.Overlay) > 0 {
		overlay, _ := json.Marshal(map[string]any{"Replace": program.Overlay})
		path := filepath.Join(directory, program.Name+".overlay.json")
		if err := os.WriteFile(path, overlay, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-overlay", path)
	}
	build := exec.Command("go", append(args, "./cmd/desktop")...)
	build.Dir = moduleRoot(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/desktop (%s): %v\n%s", program.Name, err, output)
	}
	programs.built[program.Name] = binary
	programs.versions[binary] = program.Version
	return binary
}

// Bridge builds the program of the bridge for the programs of the author once, with the version of the backend it goes
// beside (specs/2026-11-15-desk-mcp.md MC1.1), and answers its path.
func (programs *Programs) Bridge(t testing.TB, version string) string {
	t.Helper()
	programs.mu.Lock()
	defer programs.mu.Unlock()
	name := "mcp-" + version
	if binary, ok := programs.built[name]; ok {
		return binary
	}
	binary := filepath.Join(programs.directoryLocked(t), "canonkeeper-"+name)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "-ldflags=-s -w -X main.version="+version, "./cmd/desktop-mcp")
	build.Dir = moduleRoot(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/desktop-mcp: %v\n%s", err, output)
	}
	programs.built[name] = binary
	return binary
}

// Version is the version a built program carries: the shell of the same version names it in start.
func (programs *Programs) Version(binary string) string {
	programs.mu.Lock()
	defer programs.mu.Unlock()
	return programs.versions[binary]
}

// Remove removes the programs; TestMain calls it once the tests ended.
func (programs *Programs) Remove() {
	programs.mu.Lock()
	defer programs.mu.Unlock()
	if programs.directory != "" {
		_ = os.RemoveAll(programs.directory)
	}
}

// moduleRoot is the directory of the module of the backend, wherever the test runs in it.
func moduleRoot(t testing.TB) string {
	t.Helper()
	output, err := exec.Command("go", "env", "GOMOD").Output()
	module := strings.TrimSpace(string(output))
	if err != nil || module == "" || module == os.DevNull {
		t.Fatalf("the module of the backend is not found: %v", err)
	}
	return filepath.Dir(module)
}
