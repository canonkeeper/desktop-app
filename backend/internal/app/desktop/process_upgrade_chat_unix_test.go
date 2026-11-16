// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// closedInputOf counts what a turn of the chat keeps while it lives: its closed input and the binding of its model.
func closedInputOf(t *testing.T, data, jobID string) (inputs, bindings int) {
	t.Helper()
	connection := database(t, data)
	defer connection.Close(context.Background())
	if err := connection.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM public.agent_workflow_inputs WHERE job_id = $1),
		       (SELECT count(*) FROM public.job_model_bindings WHERE job_id = $1)`, jobID).Scan(&inputs, &bindings); err != nil {
		t.Fatal(err)
	}
	return inputs, bindings
}

// specs/2026-11-13-desk-upgrade.md UG1.1: the earlier program dies while a turn of the chat is canceling — the author
// pressed «Остановить», and the stop did not reach the app-server; the next program knows a migration that changes the
// table of the Jobs. It migrates over the canceling turn, and its worker ends the cancel the standard way of a turn: the
// turn is canceled, its closed input and the binding of its model are gone.
func TestAnUpdateEndsACancelingTurnOfTheChatTheEarlierProgramLeft(t *testing.T) {
	binary, resources := desktopBinary(t)
	last := lastVersion(t)
	data := filepath.Join(t.TempDir(), "data")
	provider := &providerOfTheTest{}
	stub := httptest.NewServer(provider)
	defer stub.Close()

	first := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: desktoptest.Waited(60 * time.Second)}}
	if response := window.open(first.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	for deadline := desktoptest.Deadline(90 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if waits, ok := window.waiting(project.ID); ok && waits == "helper" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the book was never read")
		}
	}
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	// The helper is connected: the reading waits for the consent once the text is indexed; the catch-up queues no cards Job
	// for a saga without a card (specs/2026-11-15-desk-reading-queue.md RQ2.1).
	readingSettled(t, data, project.ID, 90*time.Second)
	if waits, _ := window.waiting(project.ID); waits != "consent" {
		t.Fatalf("the reading with the helper waits for %q", waits)
	}
	response, body = window.post("/api/v1/projects/"+project.ID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, "chat-of-the-update")
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}
	turn, status, body := window.ask(chat.ID, longQuestion, "question-of-the-update")
	if status != http.StatusAccepted || turn == "" {
		t.Fatalf("createChatTurn = %d %s", status, body)
	}
	provider.holding(t, 2*time.Minute)

	// The stop does not reach the app-server: it is stopped. The cancel writes canceling and waits for the interrupt.
	server := appServer(t, first)
	if server == 0 {
		t.Fatal("no app-server of the run")
	}
	csrf := window.csrf()
	if err := syscall.Kill(server, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	go func() {
		request, _ := http.NewRequest(http.MethodPost, window.origin+"/api/v1/jobs/"+turn+"/cancel", nil)
		request.Header.Set("Origin", window.origin)
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: "ck_session", Value: window.cookie})
		if response, err := (&http.Client{Timeout: desktoptest.Waited(2 * time.Minute)}).Do(request); err == nil {
			_ = response.Body.Close()
		}
	}()
	deadline := desktoptest.Deadline(time.Minute)
	for statusesOf(t, data, turn)[0] != "canceling" {
		if time.Now().After(deadline) {
			t.Fatalf("the turn is %s, not canceling", statusesOf(t, data, turn)[0])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.command.Wait()
	_ = syscall.Kill(server, syscall.SIGKILL)
	if statuses := statusesOf(t, data, turn); statuses[0] != "canceling" {
		t.Fatalf("the turn left by the kill is %s", statuses[0])
	}
	if inputs, bindings := closedInputOf(t, data, turn); inputs == 0 || bindings == 0 {
		t.Fatalf("the canceling turn keeps %d inputs and %d bindings before the update", inputs, bindings)
	}

	updated := launchBackend(t, program(t, programChangingJobs), resources, data, nil)
	if updated.ready.Type != messageReady {
		t.Fatalf("the update with a canceling turn answered %+v", updated.ready)
	}
	defer updated.stop()
	if complete, _ := copies(t, data); len(complete) != 1 || !strings.HasPrefix(complete[0], "pre-") {
		t.Fatalf("the copies of the update = %v", complete)
	}
	if schema := schemaOf(t, administrator(t, data, databaseName)); schema != last+1 {
		t.Fatalf("the schema after the update = %d", schema)
	}
	window = &client{t: t, origin: updated.origin(), http: &http.Client{Timeout: desktoptest.Waited(60 * time.Second)}}
	if response := window.open(updated.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open after the update = %d", response.StatusCode)
	}
	if status := window.jobEnded(turn, 4*time.Minute); status != "canceled" {
		t.Fatalf("the canceling turn after the update = %s", status)
	}
	if inputs, bindings := closedInputOf(t, data, turn); inputs != 0 || bindings != 0 {
		t.Fatalf("the canceled turn keeps %d inputs and %d bindings", inputs, bindings)
	}
}
