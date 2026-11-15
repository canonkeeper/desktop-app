// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

// askBridge runs the program of the bridge as a program of the author does, with the environment Codex gives a server,
// sends initialize and one call and answers the text of the call and whether it is an error.
func askBridge(t *testing.T, bridge, data, tool string) (string, bool) {
	t.Helper()
	return askBridgeAs(t, bridge, data, tool, "Codex")
}

// askBridgeAs is askBridge from a program that gives its own title on initialize.
func askBridgeAs(t *testing.T, bridge, data, tool, title string) (string, bool) {
	t.Helper()
	command := exec.Command(bridge, data)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The program keeps stdin open while it waits for the answer, as a program of the author does.
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = stdin.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"codex-mcp-client","title":` + strconv.Quote(title) + `}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}` + "\n"))
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 4<<20)
	deadline := time.After(30 * time.Second)
	lines := make(chan string)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the bridge ended without the answer of the call")
			}
			var message struct {
				ID     int `json:"id"`
				Result struct {
					Content []struct{ Text string } `json:"content"`
					IsError bool                    `json:"isError"`
				} `json:"result"`
			}
			if json.Unmarshal([]byte(line), &message) == nil && message.ID == 2 && len(message.Result.Content) == 1 {
				return message.Result.Content[0].Text, message.Result.IsError
			}
		case <-deadline:
			t.Fatal("no answer of the bridge")
		}
	}
}

func programAccessOfTheWindow(t *testing.T, window *client) map[string]any {
	t.Helper()
	response, body := window.do(http.MethodGet, "/api/v1/desktop/programs", "", nil)
	var state map[string]any
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil {
		t.Fatalf("getProgramAccess = %d %s", response.StatusCode, body)
	}
	return state
}

func turnPrograms(t *testing.T, window *client, enabled bool) map[string]any {
	t.Helper()
	response, body := window.do(http.MethodPut, "/api/v1/desktop/programs", window.csrf(), map[string]bool{"enabled": enabled})
	var state map[string]any
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil {
		t.Fatalf("updateProgramAccess = %d %s", response.StatusCode, body)
	}
	return state
}

// specs/2026-11-15-desk-mcp.md MC1.2, MC1.6, MC1.7, MC3.1: the built backend of the app opens the door only when the author
// lets the programs read — a data directory whose socket path is longer than 103 bytes in Cyrillic puts the socket in a
// directory ck-mcp-* of its own — and the program of the bridge reads the saga through it, touching neither the cluster nor
// settings.json; turned off, and at the stop of the app, mcp.json, the socket and its directory are gone, and the bridge says
// the app is closed or the reading is off.
func TestTheDoorOfTheProgramsLivesWithTheApp(t *testing.T) {
	binary, resources := desktopBinary(t)
	version := versionOfProgram(binary)
	if version == "" {
		version = "dev"
	}
	bridge := programs.Bridge(t, version)
	data := filepath.Join(t.TempDir(), strings.Repeat("данные-саги-", 6))
	running := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: running.origin(), http: &http.Client{Timeout: 30 * time.Second}}
	if response := window.open(running.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"}); response.StatusCode != http.StatusCreated {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	doorFile := filepath.Join(data, programtools.DoorFileName)
	if state := programAccessOfTheWindow(t, window); state["enabled"] != false || state["open"] != false {
		t.Fatalf("by default: %v", state)
	}
	if _, err := os.Stat(doorFile); !os.IsNotExist(err) {
		t.Fatal("mcp.json lies while the reading is off")
	}
	if text, isError := askBridge(t, bridge, data, "sagas"); text != programtools.WordsClosed || !isError {
		t.Fatalf("the bridge of a closed door: %q", text)
	}

	if state := turnPrograms(t, window, true); state["enabled"] != true || state["open"] != true || state["failed"] != false {
		t.Fatalf("on: %v", state)
	}
	raw, err := os.ReadFile(doorFile)
	if err != nil {
		t.Fatal(err)
	}
	var file programtools.DoorFile
	if err := json.Unmarshal(raw, &file); err != nil || !strings.HasPrefix(filepath.Base(filepath.Dir(file.Socket)), "ck-mcp-") {
		t.Fatalf("mcp.json of a long data directory: %s", raw)
	}
	settingsBefore, _ := os.ReadFile(filepath.Join(data, "settings.json"))
	pidBefore, _ := os.ReadFile(filepath.Join(DataDir{Root: data}.Cluster(), "postmaster.pid"))
	text, isError := askBridge(t, bridge, data, "sagas")
	if isError || !strings.Contains(text, "«Северный предел»") {
		t.Fatalf("sagas through the bridge: %q", text)
	}
	settingsAfter, _ := os.ReadFile(filepath.Join(data, "settings.json"))
	pidAfter, _ := os.ReadFile(filepath.Join(DataDir{Root: data}.Cluster(), "postmaster.pid"))
	if !bytes.Equal(settingsBefore, settingsAfter) || !bytes.Equal(pidBefore, pidAfter) || len(pidBefore) == 0 {
		t.Fatal("the bridge touched settings.json or the cluster")
	}
	state := programAccessOfTheWindow(t, window)
	seen, _ := state["programs"].([]any)
	last, _ := state["lastRead"].(map[string]any)
	if len(seen) != 1 || seen[0].(map[string]any)["name"] != "Codex" || seen[0].(map[string]any)["matches"] != true || last["name"] != "Codex" {
		t.Fatalf("who turned to the door and read: %v", state)
	}

	// The log of the run and the errors of the backend name no path of the socket, no title of a saga, no name of a program
	// and no text a reading gave (MC4.5).
	const program = "Программа-метка-журнала"
	if text, isError := askBridgeAs(t, bridge, data, "sagas", program); isError || !strings.Contains(text, "«Северный предел»") {
		t.Fatalf("sagas through the bridge of a program with a title: %q", text)
	}
	if state := programAccessOfTheWindow(t, window); state["lastRead"].(map[string]any)["name"] != program {
		t.Fatalf("the last reading of the program with a title: %v", state)
	}
	var journal bytes.Buffer
	logs, err := os.ReadDir(filepath.Join(data, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		raw, _ := os.ReadFile(filepath.Join(data, "logs", entry.Name()))
		journal.Write(raw)
	}
	journal.Write(running.stderr.Bytes())
	if journal.Len() == 0 {
		t.Fatal("no log of the run to look at")
	}
	for _, secret := range []string{file.Socket, filepath.Base(filepath.Dir(file.Socket)), "Северный предел", program, "codex-mcp-client"} {
		if bytes.Contains(journal.Bytes(), []byte(secret)) {
			t.Fatalf("the log of the run names %q", secret)
		}
	}

	if state := turnPrograms(t, window, false); state["enabled"] != false || state["open"] != false || state["ever"] != true {
		t.Fatalf("off: %v", state)
	}
	if _, err := os.Stat(doorFile); !os.IsNotExist(err) {
		t.Fatal("mcp.json stayed after the reading was turned off")
	}
	if _, err := os.Stat(filepath.Dir(file.Socket)); !os.IsNotExist(err) {
		t.Fatal("the socket and its directory stayed after the reading was turned off")
	}
	if text, _ := askBridge(t, bridge, data, "sagas"); text != programtools.WordsClosed {
		t.Fatalf("the bridge after the reading was turned off: %q", text)
	}

	turnPrograms(t, window, true)
	raw, _ = os.ReadFile(doorFile)
	_ = json.Unmarshal(raw, &file)
	running.stop()
	if _, err := os.Stat(doorFile); !os.IsNotExist(err) {
		t.Fatal("mcp.json stayed after the stop of the app")
	}
	if _, err := os.Stat(filepath.Dir(file.Socket)); !os.IsNotExist(err) {
		t.Fatal("the socket stayed after the stop of the app")
	}
}

// MC3.1, MC1.7: a start with the reading on in settings.json — the author turned it on once and restarted the app — opens the
// door once the backend serves, and the bridge reads through it.
func TestAStartWithTheReadingOnOpensTheDoor(t *testing.T) {
	binary, resources := desktopBinary(t)
	version := versionOfProgram(binary)
	if version == "" {
		version = "dev"
	}
	bridge := programs.Bridge(t, version)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "settings.json"), []byte(`{"programs":{"enabled":true,"ever":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	running := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: running.origin(), http: &http.Client{Timeout: 30 * time.Second}}
	if response := window.open(running.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for state := programAccessOfTheWindow(t, window); state["open"] != true; state = programAccessOfTheWindow(t, window) {
		if time.Now().After(deadline) {
			t.Fatalf("the door did not open at a start with the reading on: %v", state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(data, programtools.DoorFileName)); err != nil {
		t.Fatal("no mcp.json at a start with the reading on")
	}
	// The door answers: the author of a fresh data directory has no saga yet — not «закрыт или выключено».
	if text, _ := askBridge(t, bridge, data, "sagas"); text != programtools.WordsNoSagas {
		t.Fatalf("the bridge at a start with the reading on: %q", text)
	}
	running.stop()
}
