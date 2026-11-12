// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"io"
	"log"
	"os"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/config"
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
	model, err := modelapi.New(modelapi.Config{Model: chatModel, CardsModel: cardsModel})
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

// specs/2026-11-12-desk-window.md WN2.1: the chat of the app — a build without the thread layer — offers its model no
// saga.threads, and the same build given the layer offers it: the set follows the layer of the build where the build
// composes its chat, not a word of the chat itself.
func TestTheChatOfTheAppOffersTheThreadsOnlyWithTheLayer(t *testing.T) {
	desktop := &build{host: "127.0.0.1:1", codes: domain.NewOpenCodes(time.Now), files: webFiles()}
	extension := desktop.extension(nil)
	if tools := chatToolsOf(t, extension); slices.Contains(tools, "saga.threads") || !slices.Contains(tools, "saga.search") {
		t.Fatalf("the chat of the app offers %v", tools)
	}
	extension.Workers.Layer = 1
	if tools := chatToolsOf(t, extension); !slices.Contains(tools, "saga.threads") {
		t.Fatalf("the chat of a build with the layer offers %v", tools)
	}
}
