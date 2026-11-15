// SPDX-License-Identifier: AGPL-3.0-only

package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

var (
	builtOnce sync.Once
	built     string
	buildErr  error
)

// program builds canonkeeper-mcp once, with a silence of the test.
func program(t *testing.T) string {
	t.Helper()
	builtOnce.Do(func() {
		directory, err := os.MkdirTemp("", "ck-bridge-build-")
		if err != nil {
			buildErr = err
			return
		}
		built = filepath.Join(directory, "canonkeeper-mcp")
		command := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version=1.4.0 -X main.silence=700ms", "-o", built, "canonkeeper/backend/cmd/desktop-mcp")
		output, err := command.CombinedOutput()
		if err != nil {
			buildErr = errors.New(string(output))
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return built
}

// running is the bridge as a process with the environment Codex gives a server (HOME, PATH, TMPDIR, LANG) and an XDG_DATA_HOME
// of another place: the bridge must read neither.
type running struct {
	t      *testing.T
	home   string
	cmd    *exec.Cmd
	in     io.WriteCloser
	lines  chan string
	exited chan error
}

func start(t *testing.T, arguments ...string) *running {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(program(t), arguments...)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TMPDIR=" + home, "LANG=C.UTF-8", "XDG_DATA_HOME=" + filepath.Join(home, "elsewhere")}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &running{t: t, home: home, cmd: cmd, in: in, lines: make(chan string, 64), exited: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
		for scanner.Scan() {
			process.lines <- scanner.Text()
		}
		close(process.lines)
		process.exited <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
	})
	return process
}

// madeNothing fails when the bridge made a file or a directory in its HOME and TMPDIR (one directory of the test).
func (process *running) madeNothing() {
	process.t.Helper()
	if made := snapshot(process.t, process.home); len(made) != 1 {
		process.t.Fatalf("the bridge made files in HOME or TMPDIR: %v", made)
	}
}

func (process *running) send(line string) {
	process.t.Helper()
	if _, err := io.WriteString(process.in, line+"\n"); err != nil {
		process.t.Fatal(err)
	}
}

// next is the next line of stdout, every one of them a message of the protocol.
func (process *running) next() map[string]any {
	process.t.Helper()
	select {
	case line, ok := <-process.lines:
		if !ok {
			process.t.Fatal("the bridge closed its stdout")
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil || decoded["jsonrpc"] != "2.0" {
			process.t.Fatalf("not a message on stdout: %q", line)
		}
		return decoded
	case <-time.After(5 * time.Second):
		process.t.Fatal("no answer")
		return nil
	}
}

// quiet says nothing came on stdout for a while.
func (process *running) quiet(wait time.Duration) {
	process.t.Helper()
	select {
	case line := <-process.lines:
		process.t.Fatalf("an answer that should not be: %q", line)
	case <-time.After(wait):
	}
}

func (process *running) exitCode(wait time.Duration) int {
	process.t.Helper()
	select {
	case err := <-process.exited:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			process.t.Fatal(err)
		}
		return 0
	case <-time.After(wait):
		process.t.Fatal("the bridge did not exit")
		return -1
	}
}

func errorCode(message map[string]any) float64 {
	failure, _ := message["error"].(map[string]any)
	code, _ := failure["code"].(float64)
	return code
}

func initialize(process *running, name, title string) {
	process.t.Helper()
	process.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"` + name + `","title":"` + title + `","version":"1"}}}`)
	if result, _ := process.next()["result"].(map[string]any); result["protocolVersion"] != "2025-06-18" {
		process.t.Fatalf("initialize: %v", result)
	}
}

// door is a door of the test on a local socket: it keeps the lines it got and answers by answer.
type door struct {
	path string
	mu   sync.Mutex
	got  []programtools.Request
}

func openDoor(t *testing.T, dataDir string, answer func(programtools.Request, net.Conn)) *door {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	opened := &door{path: socket}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request programtools.Request
				_ = json.Unmarshal(line, &request)
				opened.mu.Lock()
				opened.got = append(opened.got, request)
				opened.mu.Unlock()
				if !request.Hello {
					answer(request, conn)
				}
			}()
		}
	}()
	writeDoorFile(t, dataDir, programtools.DoorFile{Socket: socket, Fingerprint: programtools.Fingerprint(), Version: "1.4.0"})
	return opened
}

// writeDoorFile replaces mcp.json whole, as the app does: a bridge that reads it at that moment sees the old file or the new
// one, never a part.
func writeDoorFile(t *testing.T, dataDir string, file programtools.DoorFile) {
	t.Helper()
	encoded, _ := json.Marshal(file)
	path := filepath.Join(dataDir, programtools.DoorFileName)
	if err := os.WriteFile(path+".new", encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func answerWith(text string) func(programtools.Request, net.Conn) {
	return func(_ programtools.Request, conn net.Conn) {
		encoded, _ := json.Marshal(programtools.Answer{Text: text})
		_, _ = conn.Write(append(encoded, '\n'))
	}
}

func toolResult(t *testing.T, message map[string]any) (string, bool) {
	t.Helper()
	result, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("not a result: %v", message)
	}
	if _, structured := result["structuredContent"]; structured {
		t.Fatal("the bridge gave structuredContent")
	}
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("the content: %v", content)
	}
	return content[0].(map[string]any)["text"].(string), result["isError"].(bool)
}

// snapshot is every path under the roots: the bridge makes no file and no directory anywhere.
func snapshot(t *testing.T, roots ...string) []string {
	t.Helper()
	var paths []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err == nil {
				paths = append(paths, path)
			}
			return nil
		})
	}
	slices.Sort(paths)
	return paths
}

// specs/2026-11-15-desk-mcp.md MC1.1–MC1.3: the bridge as a process — wrong arguments are the code 2; before initialize only
// ping; initialize names the version of the client or the last of its list; tools/list is the set whatever the cursor, with no
// app and no mcp.json; the errors of the protocol keep it alive; stdout carries messages only; a closed stdin is the code 0.
func TestTheBridgeSpeaksTheProtocol(t *testing.T) {
	for _, arguments := range [][]string{{}, {"/a", "extension", "x"}, {"/a", "other"}, {"relative"}} {
		if code := start(t, arguments...).exitCode(5 * time.Second); code != 2 {
			t.Fatalf("%v: code %d", arguments, code)
		}
	}
	dataDir := t.TempDir()
	for _, version := range append(slices.Clone(Versions), "2026-07-28") {
		process := start(t, dataDir)
		process.send(`{"jsonrpc":"2.0","id":"d","method":"server/discover"}`)
		if message := process.next(); errorCode(message) != noMethod || message["id"] != "d" {
			t.Fatalf("server/discover: %v", message)
		}
		process.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
		if errorCode(process.next()) != noMethod {
			t.Fatal("a request before initialize")
		}
		process.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
		if message := process.next(); message["result"] == nil {
			t.Fatalf("ping: %v", message)
		}
		process.send(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"` + version + `","clientInfo":{"name":"codex-mcp-client","title":"Codex"}}}`)
		message := process.next()
		result := message["result"].(map[string]any)
		want := version
		if !slices.Contains(Versions, version) {
			want = Versions[len(Versions)-1]
		}
		info := result["serverInfo"].(map[string]any)
		if result["protocolVersion"] != want || info["name"] != "canonkeeper" || info["title"] != "CanonKeeper" || info["version"] != "1.4.0" ||
			result["instructions"] != programtools.Instructions() || message["id"] != float64(7) {
			t.Fatalf("initialize %s: %v", version, message)
		}
		if capabilities := result["capabilities"].(map[string]any); !reflect.DeepEqual(capabilities, map[string]any{"tools": map[string]any{"listChanged": false}}) {
			t.Fatalf("capabilities: %v", capabilities)
		}
		_ = process.in.Close()
		if code := process.exitCode(5 * time.Second); code != 0 {
			t.Fatalf("a closed stdin: %d", code)
		}
	}

	before := snapshot(t, dataDir)
	process := start(t, dataDir)
	initialize(process, "claude-code", "Claude Code")
	wantTools, _ := json.Marshal(programtools.Tools())
	for _, params := range []string{``, `,"params":{"cursor":"x"}`} {
		process.send(`{"jsonrpc":"2.0","id":"list","method":"tools/list"` + params + `}`)
		result := process.next()["result"].(map[string]any)
		got, _ := json.Marshal(result["tools"])
		if _, more := result["nextCursor"]; more || !jsonEqual(got, wantTools) {
			t.Fatalf("tools/list: %s", got)
		}
	}
	for name, line := range map[string]string{
		"a second initialize":       `{"jsonrpc":"2.0","id":10,"method":"initialize","params":{}}`,
		"tools/call without params": `{"jsonrpc":"2.0","id":11,"method":"tools/call"}`,
		"tools/call without a name": `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"arguments":{}}}`,
		"garbage":                   `{это не JSON`,
		"resources/list":            `{"jsonrpc":"2.0","id":13,"method":"resources/list"}`,
		"an empty array":            `[]`,
	} {
		process.send(line)
		if code := errorCode(process.next()); code == 0 {
			t.Fatalf("%s: no error", name)
		}
	}
	process.send(strings.Repeat("x", 2<<20))
	if errorCode(process.next()) != invalidReq {
		t.Fatal("a line of 2 MiB")
	}
	process.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	process.quiet(200 * time.Millisecond)
	process.send(`[{"jsonrpc":"2.0","id":"a","method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":5,"method":"ping"}]`)
	select {
	case line := <-process.lines:
		var batch []map[string]any
		if err := json.Unmarshal([]byte(line), &batch); err != nil || len(batch) != 2 {
			t.Fatalf("the answer of an array: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer of an array")
	}
	_ = process.in.Close()
	if code := process.exitCode(5 * time.Second); code != 0 {
		t.Fatalf("exit: %d", code)
	}
	if after := snapshot(t, dataDir); !slices.Equal(before, after) {
		t.Fatalf("the bridge made files: %v", after)
	}
	process.madeNothing()
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// MC1.2: silent after its start the bridge exits with the code 0; after the first message it stays.
func TestTheBridgeLeavesASilence(t *testing.T) {
	if code := start(t, t.TempDir()).exitCode(5 * time.Second); code != 0 {
		t.Fatalf("a silence: %d", code)
	}
	process := start(t, t.TempDir())
	process.send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	process.next()
	select {
	case <-process.exited:
		t.Fatal("the bridge left after its first message")
	case <-time.After(1500 * time.Millisecond):
	}
}

// MC1.4, MC1.5: a call goes to the door of mcp.json by one line — the fingerprint, the version, the kind, the program and the
// tool with its arguments — and its text comes back as it is; no mcp.json, a dead socket, another set, a connection closed
// without an answer have their words; two calls go at once; a cancelled call has no answer and its connection closes;
// initialize tells the door the program turned to it.
func TestTheBridgeTakesACallToTheDoor(t *testing.T) {
	dataDir := t.TempDir()
	process := start(t, dataDir)
	initialize(process, "claude-code", "Claude Code")
	call := func(id, tool string) {
		process.send(`{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"chapter":"1"}}}`)
	}
	call(`"no-file"`, "read")
	if text, isError := toolResult(t, process.next()); text != programtools.WordsClosed || !isError {
		t.Fatalf("no mcp.json: %q", text)
	}
	writeDoorFile(t, dataDir, programtools.DoorFile{Socket: filepath.Join(dataDir, "dead.sock"), Fingerprint: programtools.Fingerprint(), Version: "1.4.0"})
	call("2", "read")
	if text, _ := toolResult(t, process.next()); text != programtools.WordsClosed {
		t.Fatalf("a dead socket: %q", text)
	}
	for version, want := range map[string]string{"1.5.0": programtools.WordsNewSession, "1.3.0": programtools.WordsRestart} {
		writeDoorFile(t, dataDir, programtools.DoorFile{Socket: filepath.Join(dataDir, "dead.sock"), Fingerprint: "another", Version: version})
		call("3", "read")
		if text, _ := toolResult(t, process.next()); text != want {
			t.Fatalf("another set of %s: %q", version, text)
		}
	}
	text := "Глава 3 «Переправа»\n¶1 Он сказал: \"идём\" — и пошёл.\n\tконец\\"
	slowClosed := make(chan struct{}, 1)
	opened := openDoor(t, dataDir, func(request programtools.Request, conn net.Conn) {
		switch string(request.Arguments) {
		case `{"chapter":"silent"}`:
			return
		case `{"chapter":"slow"}`:
			_, _ = conn.Read(make([]byte, 1))
			slowClosed <- struct{}{}
			return
		}
		answerWith(text)(request, conn)
	})
	call("4", "read")
	if got, isError := toolResult(t, process.next()); got != text || isError {
		t.Fatalf("the text of the door: %q", got)
	}
	opened.mu.Lock()
	request := opened.got[len(opened.got)-1]
	opened.mu.Unlock()
	if request.Fingerprint != programtools.Fingerprint() || request.Version != "1.4.0" || request.Kind != programtools.KindInstall ||
		request.Client != "claude-code" || request.Title != "Claude Code" || request.Tool != "read" || string(request.Arguments) != `{"chapter":"1"}` {
		t.Fatalf("the line of the call: %+v", request)
	}
	process.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"read","arguments":{"chapter":"silent"}}}`)
	if got, _ := toolResult(t, process.next()); got != programtools.WordsNoAnswer {
		t.Fatalf("closed without an answer: %q", got)
	}
	process.send(`{"jsonrpc":"2.0","id":"slow","method":"tools/call","params":{"name":"read","arguments":{"chapter":"slow"}}}`)
	call("6", "search")
	call("7", "search")
	for range 2 {
		if got, _ := toolResult(t, process.next()); got != text {
			t.Fatalf("two at once: %q", got)
		}
	}
	process.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"slow"}}`)
	select {
	case <-slowClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled call kept its connection")
	}
	process.quiet(300 * time.Millisecond)

	// The copy in the extension says to install it anew.
	extension := start(t, dataDir, "extension")
	initialize(extension, "claude-ai", "")
	writeDoorFile(t, dataDir, programtools.DoorFile{Socket: opened.path, Fingerprint: "another", Version: "1.5.0"})
	extension.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sagas"}}`)
	if got, _ := toolResult(t, extension.next()); got != programtools.WordsReinstall {
		t.Fatalf("the extension of another set: %q", got)
	}
	// initialize told the open door that Claude Desktop turned to it — once, with the kind of its bridge and no tool.
	deadline := time.Now().Add(3 * time.Second)
	for {
		opened.mu.Lock()
		hellos := 0
		for _, request := range opened.got {
			if request.Hello && request.Client == "claude-ai" && request.Kind == programtools.KindExtension && request.Tool == "" {
				hellos++
			}
		}
		opened.mu.Unlock()
		if hellos == 1 {
			break
		}
		if hellos > 1 || time.Now().After(deadline) {
			t.Fatalf("the first words of Claude Desktop: %d", hellos)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Neither bridge made a file: the data directory holds only the mcp.json of the test, HOME and TMPDIR nothing.
	if made := snapshot(t, dataDir); !slices.Equal(made, []string{dataDir, filepath.Join(dataDir, programtools.DoorFileName)}) {
		t.Fatalf("the bridge made files in the data directory: %v", made)
	}
	process.madeNothing()
	extension.madeNothing()
}

// MC1.2, MC4 (the guard «nowhere else»): the bridge dials only a local socket and only the path mcp.json names; its imports
// carry neither net/http nor pgx nor a service.
func TestTheBridgeDialsTheSocketOfTheFileOnly(t *testing.T) {
	dataDir := t.TempDir()
	socket := filepath.Join(t.TempDir(), "door.sock")
	writeDoorFile(t, dataDir, programtools.DoorFile{Socket: socket, Fingerprint: programtools.Fingerprint(), Version: "1.4.0"})
	var dialed []string
	var mu sync.Mutex
	in, writer := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan int)
	go func() {
		done <- Run(t.Context(), in, out, Config{DataDir: dataDir, Kind: programtools.KindInstall, Version: "1.4.0", Dial: func(_ context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			dialed = append(dialed, network+" "+address)
			mu.Unlock()
			return nil, errors.New("no door")
		}})
	}()
	_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
	_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sagas"}}`+"\n")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), `"id":2`) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_ = writer.Close()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) == 0 {
		t.Fatal("the bridge dialed nothing")
	}
	for _, one := range dialed {
		if one != "unix "+socket {
			t.Fatalf("the bridge dialed %q", one)
		}
	}
	listed, err := exec.Command("go", "list", "-deps", "canonkeeper/backend/cmd/desktop-mcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range strings.Fields(string(listed)) {
		if imported == "net/http" || strings.Contains(imported, "jackc/pgx") || strings.Contains(imported, "/internal/services/") ||
			strings.Contains(imported, "/internal/repository/") || strings.Contains(imported, "/internal/store/") {
			t.Fatalf("the bridge imports %s", imported)
		}
	}
}

type lockedBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (buffer *lockedBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.text.Write(p)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.text.String()
}

// MC1.3: a call sent right after initialize, before its answer, is a call of an initialized bridge: the messages other than a
// call are taken in their order.
func TestACallRightAfterInitializeIsTaken(t *testing.T) {
	process := start(t, t.TempDir())
	process.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"codex-mcp-client"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sagas"}}`)
	for range 2 {
		message := process.next()
		if message["id"] == float64(2) {
			if text, _ := toolResult(t, message); text != programtools.WordsClosed {
				t.Fatalf("the call: %q", text)
			}
		}
	}
}
