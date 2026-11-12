package researchchat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
)

// recordChatSet writes testdata/chat-tools-with-layer.golden anew: only when the set of the chat changes on purpose.
const recordChatSet = "CANONKEEPER_CHAT_SET_RECORD"

// toolSetText is the set the model reads: every tool with its name, description and schema, in order.
func toolSetText(t *testing.T, tools []agent.ToolSpec) string {
	t.Helper()
	type tool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	set := make([]tool, 0, len(tools))
	for _, spec := range tools {
		set = append(set, tool{Name: spec.Namespace + "." + spec.Name, Description: spec.Description, InputSchema: spec.InputSchema})
	}
	body, err := json.MarshalIndent(set, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(body) + "\n"
}

// specs/2026-11-12-desk-window.md WN2.1: the chat of a build with the thread layer — the site — offers the set it offered
// before the slice to the letter (testdata/chat-tools-with-layer.golden, recorded on the tree before it); a build without
// the layer — the desktop — offers the same set without saga.threads.
func TestTheChatOffersTheThreadsOnlyWithTheLayer(t *testing.T) {
	path := filepath.Join("testdata", "chat-tools-with-layer.golden")
	site := toolSetText(t, Bundle(readServiceStub{}, memoryStub{}, true).Tools)
	if os.Getenv(recordChatSet) == "1" {
		if err := os.WriteFile(path, []byte(site), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if site != string(golden) {
		t.Fatalf("the set of the chat with the layer is not the recorded one:\n%s", site)
	}

	var without []agent.ToolSpec
	for _, spec := range Bundle(readServiceStub{}, memoryStub{}, true).Tools {
		if spec.Name != "threads" {
			without = append(without, spec)
		}
	}
	if desktop := toolSetText(t, Bundle(readServiceStub{}, memoryStub{}, false).Tools); desktop != toolSetText(t, without) {
		t.Fatalf("the set of the chat without the layer:\n%s", desktop)
	}
}
