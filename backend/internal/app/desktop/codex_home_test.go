// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// specs/2026-11-13-desk-home.md HM1.1: the app copies the profile of its bundle into the homes of Codex.
func TestTheAppCopiesTheProfileIntoTheHomesOfCodex(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	data, resources := DataDir{Root: filepath.Join(t.TempDir(), "data")}, Resources{Root: filepath.Join(t.TempDir(), "bundle")}
	cfg := desktopConfig(data, resources, &Cluster{data: data}, listener)
	if !cfg.CodexHomeCopies || cfg.CodexRepositoryHome != resources.CodexHomes() || cfg.CodexHome != data.CodexHome() {
		t.Fatalf("the Codex homes of the app = %q from %q, copies %v", cfg.CodexHome, cfg.CodexRepositoryHome, cfg.CodexHomeCopies)
	}
}

// bundleAt lays the bundle of the test at a new place, as a new mount of an AppImage or a moved installation does: the
// profiles codex-homes/ copied, file by file, the programs linked to the bundle's — PostgreSQL finds its lib and share by
// the real path. Linux only.
func bundleAt(t *testing.T, resources, place string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the bundle of the test links its programs")
	}
	if err := os.MkdirAll(place, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"postgres", "codex"} {
		if err := os.Symlink(filepath.Join(resources, part), filepath.Join(place, part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(filepath.Join(place, "codex-homes"), os.DirFS(filepath.Join(resources, "codex-homes"))); err != nil {
		t.Fatal(err)
	}
	return place
}

// homesWithoutLinks checks that the home of every flow under codex/home holds the profile of the bundle as files: no
// entry of the profile is a link or holds one (HM1.3).
func homesWithoutLinks(t *testing.T, data string) {
	t.Helper()
	homes := DataDir{Root: data}.CodexHome()
	flows, err := os.ReadDir(homes)
	if err != nil || len(flows) == 0 {
		t.Fatalf("the homes of Codex = %v, %v", flows, err)
	}
	for _, flow := range flows {
		if _, err := os.Stat(filepath.Join(homes, flow.Name(), "config.toml")); err != nil {
			t.Fatalf("the home of %s has no profile: %v", flow.Name(), err)
		}
		for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"} {
			err := filepath.WalkDir(filepath.Join(homes, flow.Name(), name), func(path string, entry fs.DirEntry, err error) error {
				if err == nil && entry.Type()&fs.ModeSymlink != 0 {
					t.Fatalf("%s is a link", path)
				}
				return err
			})
			if err != nil && !(name == "agents" && os.IsNotExist(err)) {
				t.Fatal(err)
			}
		}
	}
}

// HM1.4: the app started from its bundle at one place, stopped, and started from the same bundle at another — the first
// place gone — is ready and finds what the author made; the homes of Codex hold no link after either start.
func TestTheAppStartsFromItsBundleAtANewPlace(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	first := bundleAt(t, resources, filepath.Join(t.TempDir(), ".mount_CanonKeeper1"))
	process := startBackend(t, binary, first, data, nil)
	makeSaga(t, process)
	process.stop()
	homesWithoutLinks(t, data)

	second := bundleAt(t, resources, filepath.Join(t.TempDir(), ".mount_CanonKeeper2"))
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	moved := launchBackend(t, binary, second, data, nil)
	if moved.ready.Type != messageReady {
		t.Fatalf("the start from the moved bundle answered %+v", moved.ready)
	}
	homesWithoutLinks(t, data)
	browser := &client{t: t, origin: moved.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := browser.open(moved.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := browser.do(http.MethodGet, "/api/v1/projects", "", nil); response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Северный предел") {
		t.Fatalf("the saga after the move = %d %s", response.StatusCode, body)
	}
	moved.stop()
}
