// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/llmgateway"
	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/config"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	domain "canonkeeper/backend/internal/domain/desktop"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
)

// chatToolsOf composes a build of the app over a core that connects nothing and names the tools its chat offers the model.
func chatToolsOf(t *testing.T, extension app.Extension) []string {
	t.Helper()
	db := &storedb.DB{}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	model, err := modelapi.New(modelapi.Config{Model: llmgateway.CardsModel, CardsModel: llmgateway.CardsModel})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := app.ChatTools(app.Core{
		Config: config.Config{AllowedOrigins: []string{"http://127.0.0.1:1"}}, Logger: log.New(io.Discard, "", 0), DB: db,
		Files: root, Codex: &app.CodexClients{}, Model: model, Jobs: jobstore.New(db), Library: librarystore.New(db),
	}, extension)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

// specs/2026-11-12-desk-window.md WN2.1, specs/2026-11-13-desk-gateway.md GW3.4: the app reads the threads of a saga — its
// build has the thread layer with its one worker — so its chat offers the model saga.threads; the same build without the
// layer would not: the set follows the layer of the build where the build composes its chat, not a word of the chat.
func TestTheChatOfTheAppOffersTheThreadsOfItsLayer(t *testing.T) {
	desktop := &build{host: "127.0.0.1:1", codes: domain.NewOpenCodes(time.Now), files: webFiles(), gateway: gatewayOfTheTest(t), updates: updaterOfTheBuild(), obsidian: bridgeOfTheTest(), programs: programDoorOfTheTest(t)}
	extension := desktop.extension(nil)
	if extension.Workers.Layer != 1 {
		t.Fatalf("the workers of the thread layer of the app = %d, want 1", extension.Workers.Layer)
	}
	if tools := chatToolsOf(t, extension); !slices.Contains(tools, "saga.threads") || !slices.Contains(tools, "saga.search") {
		t.Fatalf("the chat of the app offers %v", tools)
	}
	extension.Workers.Layer = 0
	if tools := chatToolsOf(t, extension); slices.Contains(tools, "saga.threads") {
		t.Fatalf("the chat of a build without the layer offers %v", tools)
	}
}

// gatewayOfTheTest is a gateway of «Свой ИИ» for a build composed without starting anything: nobody calls it.
func gatewayOfTheTest(t *testing.T) *llmgateway.Gateway {
	t.Helper()
	gateway, err := llmgateway.Open(ownAI{}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	return gateway
}

// specs/2026-11-14-desk-journal.md JR1.3: every tool the chat of the app offers names what it shows by the one table of the
// chat — a new tool without its naming fails here before it gives the model a word unnamed.
func TestEveryToolOfTheChatOfTheAppNamesWhatItShows(t *testing.T) {
	raw, err := os.ReadFile("../../services/researchchat/testdata/named-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Tools map[string]string `json:"tools"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	desktop := &build{host: "127.0.0.1:1", codes: domain.NewOpenCodes(time.Now), files: webFiles(), gateway: gatewayOfTheTest(t), updates: updaterOfTheBuild(), obsidian: bridgeOfTheTest(), programs: programDoorOfTheTest(t)}
	tools := chatToolsOf(t, desktop.extension(nil))
	if len(tools) < 10 {
		t.Fatalf("the chat of the app offers %v", tools)
	}
	for _, tool := range tools {
		if table.Tools[tool] == "" {
			t.Errorf("the tool %s of the chat of the app names nothing", tool)
		}
	}
}

// specs/2026-11-15-desk-mcp.md MC2.1, MC2.2: the set of the programs of the author is the chat of the app without what calls a
// model, changes the saga or reads a chat — scan, remember, attachment — and with sagas. A new tool of the chat fails here
// until it is put on one side: in the set or among these three.
func TestTheSetOfTheProgramsIsTheChatOfTheAppWithoutWhatCallsOrChanges(t *testing.T) {
	desktop := &build{host: "127.0.0.1:1", codes: domain.NewOpenCodes(time.Now), files: webFiles(), gateway: gatewayOfTheTest(t), updates: updaterOfTheBuild(), obsidian: bridgeOfTheTest(), programs: programDoorOfTheTest(t)}
	closed := []string{"scan", "remember", "attachment"}
	var want []string
	for _, tool := range chatToolsOf(t, desktop.extension(nil)) {
		name := strings.TrimPrefix(tool, "saga.")
		if !slices.Contains(closed, name) {
			want = append(want, name)
		}
	}
	want = append(want, programtools.Sagas)
	got := programtools.Names()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the set of the programs %v is not the chat of the app %v without %v and with sagas", got, want, closed)
	}
}
