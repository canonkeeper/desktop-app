// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
)

// doorText is what the door of the test gives: lines, quotes and a backslash, as a reading gives them.
const doorText = "Саги автора в CanonKeeper: 1.\n1. «Северный предел» — saga: \"11111111\"; книг: 2, глав: 40\n"

// programModel is a stub provider of Chat Completions that calls the tool sagas of the server canonkeeper by its name in
// the request — the tool the client declared from the list of the bridge — and answers once the tool answered.
type programModel struct {
	mu      sync.Mutex
	offered []string
	results []string
}

func (model *programModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []chatMessage `json:"messages"`
		Tools    []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 {
		http.Error(w, "not a request of Chat Completions", http.StatusBadRequest)
		return
	}
	sagas := ""
	model.mu.Lock()
	for _, tool := range body.Tools {
		model.offered = append(model.offered, tool.Function.Name)
		if strings.Contains(tool.Function.Name, "canonkeeper") && strings.HasSuffix(tool.Function.Name, "sagas") {
			sagas = tool.Function.Name
		}
	}
	last := body.Messages[len(body.Messages)-1]
	if last.Role == "tool" {
		model.results = append(model.results, last.text())
	}
	model.mu.Unlock()
	usage := `"usage":{"prompt_tokens":1000,"completion_tokens":20}`
	w.Header().Set("Content-Type", "text/event-stream")
	switch {
	case last.Role == "tool" || sagas == "":
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Готово."},"finish_reason":"stop"}],`+usage+"}\n\n")
	default:
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[`+
			`{"index":0,"id":"call-sagas","type":"function","function":{"name":`+strconv.Quote(sagas)+`,"arguments":"{}"}}]},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],`+usage+"}\n\n")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// specs/2026-11-15-desk-mcp.md MC1.3, MC1.4: a real client — codex-app-server of the fork over a copy of the profile of the
// chat with the server canonkeeper added by an override (the program of the bridge and the data directory of the test) —
// takes the tool sagas from the list of the bridge, the stub model calls it by that name, and the text a door of the test
// gives reaches the model through the real bridge as it is. It skips without the binary of the fork.
func TestARealClientReadsThroughTheBridge(t *testing.T) {
	if os.Getenv("CK_CODEX_RUNTIME_TEST_BINARY") == "" {
		t.Skip("set CK_CODEX_RUNTIME_TEST_BINARY to the pinned fork binary")
	}
	bridge := filepath.Join(t.TempDir(), "canonkeeper-mcp")
	build := exec.Command("go", "build", "-trimpath", "-o", bridge, "canonkeeper/backend/cmd/desktop-mcp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/desktop-mcp: %v\n%s", err, output)
	}
	dataDir := t.TempDir()
	socket := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var calls sync.WaitGroup
	called := make(chan programtools.Request, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			go func() {
				defer calls.Done()
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request programtools.Request
				if json.Unmarshal(line, &request) != nil || request.Hello {
					return
				}
				called <- request
				answer, _ := json.Marshal(programtools.Answer{Text: doorText})
				_, _ = conn.Write(append(answer, '\n'))
			}()
		}
	}()
	door, _ := json.Marshal(programtools.DoorFile{Socket: socket, Fingerprint: programtools.Fingerprint(), Version: "dev"})
	if err := os.WriteFile(filepath.Join(dataDir, programtools.DoorFileName), door, 0o600); err != nil {
		t.Fatal(err)
	}

	stand := openStand(t)
	model := &programModel{}
	stand.settings.set(t, custom(startProvider(t, model), "chat"), "key-test-runtime-0003")
	client := runtimeChat(t, stand, 128_000,
		fmt.Sprintf("mcp_servers.canonkeeper.command=%q", bridge),
		fmt.Sprintf("mcp_servers.canonkeeper.args=[%q]", dataDir),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	sagaCalls := 0
	result, err := client.Run(ctx, turnRequest(stand, &sagaCalls))
	if err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	offered, results := append([]string(nil), model.offered...), append([]string(nil), model.results...)
	model.mu.Unlock()
	if result.Status != agent.TurnCompleted {
		t.Fatalf("the turn = %+v; the model was offered %v", result, offered)
	}
	select {
	case request := <-called:
		if request.Tool != programtools.Sagas || request.Kind != programtools.KindInstall || request.Fingerprint != programtools.Fingerprint() {
			t.Fatalf("the line of the call: %+v", request)
		}
	default:
		t.Fatalf("the door got no call; the model was offered %v", offered)
	}
	if len(results) != 1 || !strings.Contains(results[0], "«Северный предел» — saga: \"11111111\"") {
		t.Fatalf("the text of the door did not reach the model: %q", results)
	}
}
