package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProcessTransportLogsStderrTailOnUnexpectedExit(t *testing.T) {
	root := runtimeTestRoot(t)
	var script strings.Builder
	script.WriteString("#!/bin/sh\nprintf '{}\\n'\n")
	for line := 1; line <= 24; line++ {
		fmt.Fprintf(&script, "echo 'stderr line %02d' >&2\n", line)
	}
	fmt.Fprintf(&script, "echo 'LONG %s' >&2\nexit 7\n", strings.Repeat("y", 1000))
	output := &syncBuffer{}
	transport, err := startTransport(t.Context(), runtimeTestConfig(root, writeRuntimeScript(t, root, script.String()), output))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := transport.Receive(ctx); err != nil {
		t.Fatalf("initial frame: %v", err)
	}
	if _, err := transport.Receive(ctx); err == nil {
		t.Fatal("Receive() after exit = nil error")
	}

	event := stderrTailEvent(t, output.String())
	if event.Reason != "exited" || !strings.Contains(event.Exit, "exit_code=7") || len(event.Lines) != stderrTailLines {
		t.Fatalf("stderr event = %+v, want the last %d lines after exit 7", event, stderrTailLines)
	}
	if event.Lines[0] != "stderr line 06" || event.Lines[18] != "stderr line 24" ||
		!strings.HasPrefix(event.Lines[19], "LONG yyy") || len(event.Lines[19]) > stderrTailLineBytes {
		t.Fatalf("stderr tail lines = %q", event.Lines)
	}
}

func TestProcessTransportKeepsStderrOutOfLogOnClose(t *testing.T) {
	root := runtimeTestRoot(t)
	output := &syncBuffer{}
	script := "#!/bin/sh\necho 'PRIVATE_STDERR_CANARY' >&2\nprintf '{}\\n'\nsleep 5\n"
	transport, err := startTransport(t.Context(), runtimeTestConfig(root, writeRuntimeScript(t, root, script), output))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := transport.Receive(ctx); err != nil {
		t.Fatalf("initial frame: %v", err)
	}

	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(output.String(), "PRIVATE_STDERR_CANARY") {
		t.Fatalf("stderr of a closed app-server reached the log: %q", output.String())
	}
}

func TestStartTransportWrapsProcessStartFailure(t *testing.T) {
	root := runtimeTestRoot(t)

	_, err := startTransport(t.Context(), runtimeTestConfig(root, filepath.Join(root, "missing-codex"), &syncBuffer{}))

	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "missing-codex") {
		t.Fatalf("startTransport() error = %v, want the wrapped start failure", err)
	}
}

func TestNewClientLogsStderrTailWhenAppServerFailsToStart(t *testing.T) {
	root := runtimeTestRoot(t)
	output := &syncBuffer{}
	config := runtimeTestConfig(root, writeRuntimeScript(t, root, "#!/bin/sh\necho 'bwrap: setting up uid map: Permission denied' >&2\nexit 1\n"), output)
	repositoryHome := filepath.Join(root, "repository-home")
	if err := os.MkdirAll(filepath.Join(repositoryHome, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(repositoryHome, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, ClientConfig{
		CodexPath: config.CodexPath, CWD: config.CWD, RepositoryHome: repositoryHome, CodexHome: config.CodexHome,
		CodexSQLiteHome: config.CodexSQLiteHome, PrivateHome: config.PrivateHome, TempDir: config.TempDir,
		Logger: config.Logger,
	})

	if err != nil {
		t.Fatalf("NewClient() error = %v, want a client without runtime (R3.1)", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.RuntimeReady() {
		t.Fatal("NewClient() reports a failing app-server as ready")
	}
	event := stderrTailEvent(t, output.String())
	if len(event.Lines) != 1 || event.Lines[0] != "bwrap: setting up uid map: Permission denied" {
		t.Fatalf("stderr event = %+v", event)
	}
}

type stderrTailEventForTest struct {
	Event  string   `json:"event"`
	Reason string   `json:"reason"`
	Exit   string   `json:"exit"`
	Lines  []string `json:"lines"`
}

func stderrTailEvent(t *testing.T, output string) stderrTailEventForTest {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var event stderrTailEventForTest
		if json.Unmarshal([]byte(line), &event) == nil && event.Event == "codex_app_server_stderr" {
			return event
		}
	}
	t.Fatalf("log has no codex_app_server_stderr event: %q", output)
	return stderrTailEventForTest{}
}

func runtimeTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "cwd"), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeRuntimeScript(t *testing.T, root, body string) string {
	t.Helper()
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func runtimeTestConfig(root, codexPath string, output *syncBuffer) transportConfig {
	return transportConfig{
		CodexPath: codexPath, CWD: filepath.Join(root, "cwd"), CodexHome: filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"), PrivateHome: filepath.Join(root, "home"),
		TempDir: filepath.Join(root, "tmp"), Logger: log.New(output, "", 0),
	}
}

type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *syncBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *syncBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
