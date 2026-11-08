package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
)

func TestCleanEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "representative-backend-secret")
	root := t.TempDir()
	for _, name := range []string{"cwd", "codex-home", "sqlite", "home", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	transport, err := startTransport(t.Context(), transportConfig{
		CodexPath: script, CWD: filepath.Join(root, "cwd"), CodexHome: filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	var lines []string
	for {
		ctx, cancel := context.WithCancel(t.Context())
		frame, receiveErr := transport.Receive(ctx)
		cancel()
		if receiveErr != nil {
			if errors.Is(receiveErr, os.ErrClosed) || strings.Contains(receiveErr.Error(), "EOF") {
				break
			}
			break
		}
		lines = append(lines, string(frame))
	}
	environment := strings.Join(lines, "\n")
	if strings.Contains(environment, "DATABASE_URL") || strings.Contains(environment, "representative-backend-secret") {
		t.Fatalf("backend secret inherited by app-server: %s", environment)
	}
	for _, required := range []string{"CODEX_HOME=", "CODEX_SQLITE_HOME=", "HOME=", "TMPDIR="} {
		if !strings.Contains(environment, required) {
			t.Fatalf("clean environment lacks %s: %s", required, environment)
		}
	}
}

func TestStartTransportCreatesWritableRuntimeDirectories(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	directories := []string{
		filepath.Join(root, "sqlite"),
		filepath.Join(root, "private"),
		filepath.Join(root, "tmp"),
	}
	transport, err := startTransport(t.Context(), transportConfig{
		CodexPath: script, CWD: cwd, CodexHome: filepath.Join(root, "codex-home"),
		CodexSQLiteHome: directories[0], PrivateHome: directories[1], TempDir: directories[2],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, directory := range directories {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("runtime directory %q was not created: %v", directory, err)
		}
	}
}

func TestProcessTransportReportsUnexpectedExitWithoutStderr(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '{}\\n'\nprintf 'PRIVATE_STDERR_CANARY\\n' >&2\nsleep 0.1\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	transport, err := startTransport(t.Context(), transportConfig{
		CodexPath: script, CWD: cwd, CodexHome: filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"), PrivateHome: filepath.Join(root, "home"),
		TempDir: filepath.Join(root, "tmp"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := transport.Receive(ctx); err != nil {
		t.Fatalf("initial frame: %v", err)
	}
	_, err = transport.Receive(ctx)
	if !errors.Is(err, agentdomain.ErrRuntimeProcessExited) || !strings.Contains(err.Error(), "exit_code=7") ||
		strings.Contains(err.Error(), "PRIVATE_STDERR_CANARY") {
		t.Fatalf("unexpected process exit error = %v", err)
	}
}

func TestStartTransportPassesOverridesAndOnlyListedEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "representative-backend-secret")
	t.Setenv("PROVIDER_TEST_KEY", "provider-key-value")
	root := t.TempDir()
	for _, name := range []string{"cwd", "codex-home", "sqlite", "home", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"ARGS $*\"\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	transport, err := startTransport(t.Context(), transportConfig{
		CodexPath: script, CWD: filepath.Join(root, "cwd"), CodexHome: filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"),
		ConfigOverrides: []string{`model_provider="codex-lb"`}, PassEnv: []string{"PROVIDER_TEST_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	var lines []string
	for {
		ctx, cancel := context.WithCancel(t.Context())
		frame, receiveErr := transport.Receive(ctx)
		cancel()
		if receiveErr != nil {
			break
		}
		lines = append(lines, string(frame))
	}
	output := strings.Join(lines, "\n")
	if !strings.Contains(output, `ARGS app-server -c model_provider="codex-lb" --listen stdio://`) {
		t.Fatalf("overrides were not passed as -c flags before --listen: %s", output)
	}
	if !strings.Contains(output, "PROVIDER_TEST_KEY=provider-key-value") {
		t.Fatalf("listed provider variable was not passed: %s", output)
	}
	if strings.Contains(output, "representative-backend-secret") {
		t.Fatalf("backend secret inherited by app-server: %s", output)
	}
}

func TestStartTransportRejectsUnsafeRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	base := transportConfig{
		CodexPath: filepath.Join(root, "codex"), CWD: root, CodexHome: filepath.Join(root, "home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"), PrivateHome: filepath.Join(root, "private"), TempDir: filepath.Join(root, "tmp"),
	}
	for name, mutate := range map[string]func(*transportConfig){
		"override injects a new line": func(c *transportConfig) { c.ConfigOverrides = []string{"model=\"x\"\nsandbox_mode=\"danger\""} },
		"pass-through replaces PATH":  func(c *transportConfig) { c.PassEnv = []string{"PATH"} },
	} {
		config := base
		mutate(&config)
		if _, err := startTransport(t.Context(), config); err == nil {
			t.Errorf("%s: startTransport accepted unsafe settings", name)
		}
	}
}

// specs/2026-11-07-desktop-app.md DA1.4: the codex CLI runs the app-server as its subcommand, the codex-app-server of
// the release bundle is the app-server itself.
func TestTheAppServerOfTheBundleTakesNoSubcommand(t *testing.T) {
	for path, want := range map[string]int{
		filepath.Join("opt", "codex", "bin", "codex"):               1,
		filepath.Join("opt", "canonkeeper", "codex-app-server"):     0,
		filepath.Join("opt", "canonkeeper", "codex-app-server.exe"): 0,
	} {
		if got := len(appServerArgs(path)); got != want {
			t.Errorf("appServerArgs(%q) = %d arguments, want %d", path, got, want)
		}
	}
}
