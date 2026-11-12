// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/config"
	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"

	"github.com/go-chi/chi/v5"
)

// mountedOperations composes the desktop build over a core that connects nothing and names the operations it mounts by
// the route catalog.
func mountedOperations(t *testing.T) []string {
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
	desktop := &build{host: "127.0.0.1:1", codes: domain.NewOpenCodes(time.Now), files: webFiles()}
	routes, err := app.Routes(app.Core{
		Config: config.Config{AllowedOrigins: []string{"http://127.0.0.1:1"}}, Logger: log.New(io.Discard, "", 0), DB: db,
		Files: root, Codex: &app.CodexClients{}, Model: model, Jobs: jobstore.New(db), Library: librarystore.New(db),
	}, desktop.extension(nil))
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]string{}
	for _, route := range httpapi.RouteCatalog() {
		operations[strings.ToUpper(route.Method)+" /api/v1"+route.Path] = route.OperationID
	}
	var mounted []string
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(strings.ReplaceAll(route, "/*/", "/"), "/")
		if operation, ok := operations[method+" "+route]; ok {
			mounted = append(mounted, operation)
		} else if strings.HasPrefix(route, "/api/v1/") {
			t.Errorf("%s %s is mounted without an operation of the contract", method, route)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(mounted)
	return slices.Compact(mounted)
}

// specs/2026-11-07-desktop-app.md DA16.4: contracts/openapi.desktop.json holds the operations the desktop build mounts —
// the open core and openDesktopSession — no more and no fewer; the closed modules of the site add theirs only to
// contracts/openapi.json.
func TestTheDesktopContractIsWhatTheDesktopBuildMounts(t *testing.T) {
	mounted := mountedOperations(t)
	// «Кто автор» is open and the desktop answers it (specs/2026-11-12-desk-window.md WN1.1); signing in is the site's.
	if !slices.Contains(mounted, "openDesktopSession") || !slices.Contains(mounted, "getCurrentUser") || slices.Contains(mounted, "loginUser") {
		t.Fatalf("the desktop mounts %v", mounted)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "openapi.desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &contract); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, operations := range contract.Paths {
		for _, operation := range operations {
			if operation.OperationID != "" {
				listed = append(listed, operation.OperationID)
			}
		}
	}
	slices.Sort(listed)
	if !slices.Equal(listed, mounted) {
		encoded, _ := json.Marshal(mounted)
		t.Fatalf("contracts/openapi.desktop.json names %d operations, the desktop build mounts %d:\n%s", len(listed), len(mounted), encoded)
	}
}
