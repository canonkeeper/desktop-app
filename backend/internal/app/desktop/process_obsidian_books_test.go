// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// programWithALoadStoppedAt is the program of the tree whose load of the books of the folders stops at a point of its
// writes — after its queue, after the links of its volumes — until the test kills the process
// (specs/2026-11-14-desk-obsidian-books.md OK1.4).
func programWithALoadStoppedAt(point string) programVariant {
	return programVariant{name: "obsidian-stop-" + point, obsidianPoints: `// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "time"

var obsidianPoint = func(point string) error {
	if point == "` + point + `" {
		time.Sleep(time.Hour)
	}
	return nil
}
`}
}

// keyed is a write of the window with its Idempotency-Key; its client waits as long as the write does.
func (browser *client) keyed(method, path, key string, body any) (int, []byte, error) {
	encoded, _ := json.Marshal(body)
	request, _ := http.NewRequest(method, browser.origin+path, bytes.NewReader(encoded))
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw, nil
}

// linkedVault is a saga of the window linked with a vault of the example on the disk.
type linkedVault struct {
	project string
	vault   string
}

func (process *backend) linkedSaga(t *testing.T, window *client, notes map[string]string) linkedVault {
	t.Helper()
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	vault := filepath.Join(t.TempDir(), "Хранилище-метка-пути")
	writeNotes(t, vault, notes)
	if outcome := process.picked(process.choose(window, project.ID), vault); outcome != "linked" {
		t.Fatalf("the vault = %s", outcome)
	}
	return linkedVault{project: project.ID, vault: vault}
}

func writeNotes(t *testing.T, vault string, notes map[string]string) {
	t.Helper()
	for name, text := range notes {
		full := filepath.Join(vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// previewedBooks are the books of the folders as the preview of the window gives them: folder, fingerprint and state.
type previewedBook struct {
	Folder      string `json:"folder"`
	Title       string `json:"title"`
	Fingerprint string `json:"fingerprint"`
	State       struct {
		Kind    string `json:"kind"`
		BookID  string `json:"bookId"`
		Changed bool   `json:"changed"`
		InTrash bool   `json:"inTrash"`
	} `json:"state"`
}

func previewBooks(t *testing.T, window *client, project string, books []string) []previewedBook {
	t.Helper()
	response, body := window.do(http.MethodPost, "/api/v1/projects/"+project+"/obsidian/preview", window.csrf(), map[string]any{"books": books})
	var preview struct {
		Books []previewedBook `json:"books"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &preview) != nil || len(preview.Books) != len(books) {
		t.Fatalf("previewObsidianImport = %d %s", response.StatusCode, body)
	}
	return preview.Books
}

func loadRequest(books []previewedBook) map[string]any {
	var entries []map[string]string
	for _, book := range books {
		entries = append(entries, map[string]string{"folder": book.Folder, "fingerprint": book.Fingerprint})
	}
	return map[string]any{"books": entries}
}

// waitFor waits for an event of the run the condition reads, as long as the test of the run may take.
func waitFor(t *testing.T, what string, limit time.Duration, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); !done(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, limit)
		}
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK1.4, OK1.6, OK0.3 on the backend of the app: the backend killed between the queue
// of a load and its start — right after the queue, before the links of its volumes, or after them — leaves its draft; the
// next start cancels it — no live queue —, the same key is load_interrupted, a load under a new key goes through the queue of
// the volumes, and both folders are linked to their books; the log of the run names neither the vault nor a note.
func TestABackendKilledInTheMiddleOfALoadLeavesNoQueue(t *testing.T) {
	for _, stop := range []struct {
		point string
		links int
	}{{"after_queue", 0}, {"after_links", 2}} {
		t.Run(stop.point, func(t *testing.T) { killedInTheMiddleOfALoad(t, stop.point, stop.links) })
	}
}

func killedInTheMiddleOfALoad(t *testing.T, point string, links int) {
	resources := desktoptest.Resources(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, program(t, programWithALoadStoppedAt(point)), resources, data, nil)
	window := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: time.Minute}}
	if response := window.open(first.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the code = %d", response.StatusCode)
	}
	saga := first.linkedSaga(t, window, map[string]string{
		"Книга 1/01 Застава.md":   "Ольга вышла к заставе на рассвете. Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md": "Мирон ждал у переправы. Паром не шёл.",
		"Книга 2/01 Порт.md":      "Судно вошло в порт. Берег был пуст.",
	})
	books := previewBooks(t, window, saga.project, []string{"Книга 1", "Книга 2"})
	const key = "load-of-the-process-0001"
	go func() {
		_, _, _ = window.keyed(http.MethodPost, "/api/v1/projects/"+saga.project+"/obsidian/books", key, loadRequest(books))
	}()
	connection := database(t, data)
	waitFor(t, "the load stopped "+point, time.Minute, func() bool {
		var queues, rows int
		return connection.QueryRow(context.Background(), `
			SELECT (SELECT count(*) FROM import_queues WHERE status = 'uploading'), (SELECT count(*) FROM obsidian_books)
		`).Scan(&queues, &rows) == nil && queues == 1 && rows == links
	})
	_ = connection.Close(context.Background())
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.command.Wait()

	second := startBackend(t, program(t, programOfTheTree), resources, data, nil)
	window = &client{t: t, origin: second.origin(), http: &http.Client{Timeout: time.Minute}}
	if response := window.open(second.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the code after the restart = %d", response.StatusCode)
	}
	connection = database(t, data)
	var live, drafts int
	if err := connection.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE status IN ('uploading', 'draft', 'running')), count(*) FROM import_queues WHERE project_id = $1
	`, saga.project).Scan(&live, &drafts); err != nil || live != 0 || drafts != 1 {
		t.Fatalf("after the restart: live queues %d of %d, %v", live, drafts, err)
	}
	status, body, err := window.keyed(http.MethodPost, "/api/v1/projects/"+saga.project+"/obsidian/books", key, loadRequest(books))
	if err != nil || status != http.StatusConflict || !strings.Contains(string(body), "obsidian.load_interrupted") {
		t.Fatalf("the key of the killed load = %d %s %v", status, body, err)
	}
	status, body, err = window.keyed(http.MethodPost, "/api/v1/projects/"+saga.project+"/obsidian/books", "load-of-the-process-0002", loadRequest(books))
	var loaded struct {
		Queue *struct {
			ID string `json:"id"`
		} `json:"queue"`
		Folders []struct {
			Outcome string `json:"outcome"`
		} `json:"folders"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(body, &loaded) != nil || loaded.Queue == nil || len(loaded.Folders) != 2 ||
		loaded.Folders[0].Outcome != "queued" || loaded.Folders[1].Outcome != "queued" {
		t.Fatalf("a load under a new key = %d %s %v", status, body, err)
	}
	waitFor(t, "the books of both folders", 3*time.Minute, func() bool {
		books := previewBooks(t, window, saga.project, []string{"Книга 1", "Книга 2"})
		return books[0].State.Kind == "linked" && books[1].State.Kind == "linked" && !books[0].State.Changed && !books[1].State.Changed
	})

	logs, err := os.ReadDir(filepath.Join(data, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var journal strings.Builder
	for _, entry := range logs {
		raw, _ := os.ReadFile(filepath.Join(data, "logs", entry.Name()))
		journal.Write(raw)
	}
	for _, secret := range []string{"Хранилище-метка-пути", "Застава", "Переправа", "Книга 1"} {
		if strings.Contains(journal.String(), secret) {
			t.Fatalf("the log of the run names %q", secret)
		}
	}
	if !strings.Contains(journal.String(), `"obsidian_drafts_canceled","count":1`) {
		t.Fatalf("the log keeps no cancel of the draft:\n%s", journal.String())
	}
}

// loadedBooks is the answer of a load of the window: the queue it started and the outcome of each folder.
type loadedBooks struct {
	Queue *struct {
		ID string `json:"id"`
	} `json:"queue"`
	Folders []struct {
		Outcome string `json:"outcome"`
		BookID  string `json:"bookId"`
	} `json:"folders"`
}

// specs/2026-11-14-desk-obsidian-books.md OK0.3, OK1.2, OK2.6 on the backend of the app, where the coordinator of the
// deletions runs: the book of a volume of the bridge deleted into «Недавно удалённые» keeps its folder after the core
// detached the import Job of its volume — the folder is in_trash and a load puts no second book of it onto the shelf —, and
// the book given back holds its folder again.
func TestTheBookOfAVolumeKeepsItsFolderThroughTheTrashAndBack(t *testing.T) {
	resources := desktoptest.Resources(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, program(t, programOfTheTree), resources, data, nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: time.Minute}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the code = %d", response.StatusCode)
	}
	saga := process.linkedSaga(t, window, map[string]string{
		"Книга 1/01 Застава.md":   "Ольга вышла к заставе на рассвете. Ветер с перевала нёс снег.",
		"Книга 1/02 Переправа.md": "Мирон ждал у переправы. Паром не шёл.",
	})
	path := "/api/v1/projects/" + saga.project + "/obsidian/books"
	status, body, err := window.keyed(http.MethodPost, path, "load-of-the-trash-0001", loadRequest(previewBooks(t, window, saga.project, []string{"Книга 1"})))
	if err != nil || status != http.StatusOK {
		t.Fatalf("the load = %d %s %v", status, body, err)
	}
	connection := database(t, data)
	var bookID string
	waitFor(t, "the book of the folder and the end of its queue", 3*time.Minute, func() bool {
		var live int
		if connection.QueryRow(context.Background(), `
			SELECT count(*) FROM import_queues WHERE project_id = $1 AND status IN ('uploading', 'draft', 'running')
		`, saga.project).Scan(&live) != nil || live != 0 {
			return false
		}
		book := previewBooks(t, window, saga.project, []string{"Книга 1"})[0]
		bookID = book.State.BookID
		return book.State.Kind == "linked" && !book.State.InTrash
	})

	if response, body := window.do(http.MethodDelete, "/api/v1/books/"+bookID, window.csrf(), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("deleteBook = %d %s", response.StatusCode, body)
	}
	waitFor(t, "the Job of the volume detached by the deletion of its book", time.Minute, func() bool {
		var status string
		return connection.QueryRow(context.Background(), `
			SELECT job.status FROM obsidian_books AS link
			JOIN import_queue_items AS item ON item.id = link.item_id
			JOIN jobs AS job ON job.id = item.import_job_id
			WHERE link.project_id = $1
		`, saga.project).Scan(&status) == nil && status == "expired"
	})
	books := previewBooks(t, window, saga.project, []string{"Книга 1"})
	if state := books[0].State; state.Kind != "linked" || !state.InTrash || state.BookID != bookID {
		t.Fatalf("the folder of the book in the trash = %+v", state)
	}
	status, body, err = window.keyed(http.MethodPost, path, "load-of-the-trash-0002", loadRequest(books))
	var loaded loadedBooks
	if err != nil || status != http.StatusOK || json.Unmarshal(body, &loaded) != nil || loaded.Queue != nil || len(loaded.Folders) != 1 ||
		loaded.Folders[0].Outcome != "in_trash" || loaded.Folders[0].BookID != bookID {
		t.Fatalf("a load of the folder of the book in the trash = %d %s %v", status, body, err)
	}

	if response, body := window.do(http.MethodPost, "/api/v1/books/"+bookID+"/restore", window.csrf(), nil); response.StatusCode != http.StatusOK {
		t.Fatalf("restoreBook = %d %s", response.StatusCode, body)
	}
	if state := previewBooks(t, window, saga.project, []string{"Книга 1"})[0].State; state.Kind != "linked" || state.InTrash || state.BookID != bookID {
		t.Fatalf("the folder of the book given back = %+v", state)
	}
}
