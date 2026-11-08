package codex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeHomeUsesReadOnlyRepositoryLinks(t *testing.T) {
	repositoryHome := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(filepath.Join(repositoryHome, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repositoryHome, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(repositoryHome, name), []byte(name+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	runtimeHome := filepath.Join(t.TempDir(), "runtime-home")
	if err := PrepareHome(runtimeHome, repositoryHome); err != nil {
		t.Fatal(err)
	}
	if err := PrepareHome(runtimeHome, repositoryHome); err != nil {
		t.Fatalf("idempotent prepare: %v", err)
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"} {
		info, err := os.Lstat(filepath.Join(runtimeHome, name))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a stable link: %#v %v", name, info, err)
		}
		target, err := os.Readlink(filepath.Join(runtimeHome, name))
		if err != nil || target != filepath.Join(repositoryHome, name) {
			t.Fatalf("%s target = %q, %v", name, target, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "runtime-state"), []byte("writable"), 0o600); err != nil {
		t.Fatalf("runtime home is not writable: %v", err)
	}
}

func TestClientPreparesExternalRuntimeHome(t *testing.T) {
	repositoryHome := filepath.Join(t.TempDir(), "codex-home")
	for _, directory := range []string{"agents", "skills"} {
		if err := os.MkdirAll(filepath.Join(repositoryHome, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(repositoryHome, name), []byte(name+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	runtimeHome := filepath.Join(t.TempDir(), "runtime-home")
	transport := newFakeTransport()
	client, err := NewClient(t.Context(), ClientConfig{
		Transport: transport, CWD: "/safe/canonkeeper",
		RepositoryHome: repositoryHome, CodexHome: runtimeHome,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := os.Readlink(filepath.Join(runtimeHome, "config.toml")); err != nil {
		t.Fatalf("client did not prepare external home: %v", err)
	}
}

func TestRuntimeHomeOmitsDisabledAgentsAndRejectsStaleEntry(t *testing.T) {
	repositoryHome := filepath.Join(t.TempDir(), "interactive-home")
	if err := os.MkdirAll(filepath.Join(repositoryHome, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(repositoryHome, name), []byte(name+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	runtimeHome := filepath.Join(t.TempDir(), "runtime-home")
	if err := PrepareHome(runtimeHome, repositoryHome); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(runtimeHome, "agents")); !os.IsNotExist(err) {
		t.Fatalf("disabled agents entry exists: %v", err)
	}

	staleRuntime := filepath.Join(t.TempDir(), "stale-runtime")
	if err := os.MkdirAll(filepath.Join(staleRuntime, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PrepareHome(staleRuntime, repositoryHome); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale agents error = %v, want ErrConflict", err)
	}
}
