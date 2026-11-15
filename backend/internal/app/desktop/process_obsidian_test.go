// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
)

// choose begins a choice of the folder of a vault through the window and reads its number as the shell hears it.
func (process *backend) choose(window *client, projectID string) string {
	process.t.Helper()
	response, body := window.do(http.MethodPost, "/api/v1/projects/"+projectID+"/obsidian/choice", window.csrf(), nil)
	var choice struct {
		Request string `json:"request"`
	}
	if response.StatusCode != http.StatusAccepted || json.Unmarshal(body, &choice) != nil || len(choice.Request) != 43 {
		process.t.Fatalf("beginObsidianVaultChoice = %d %s", response.StatusCode, body)
	}
	for {
		message := process.receive(10 * time.Second)
		if message.Type == messageFolderChoice {
			if message.Request != choice.Request || message.Saga != "Северный предел" {
				process.t.Fatalf("folder-choice = %+v", message)
			}
			return choice.Request
		}
	}
}

// picked tells the backend the folder the author picked and reads how it ended the choice.
func (process *backend) picked(request, folder string) string {
	process.t.Helper()
	process.send(ControlMessage{Type: messageFolderPicked, Request: request, Path: folder})
	for {
		message := process.receive(20 * time.Second)
		if message.Type == messageFolderResult {
			if message.Request != request {
				process.t.Fatalf("folder-result of another choice: %+v", message)
			}
			return message.Outcome
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB1.1, OB1.3–OB1.5, OB2.1, OB0.4: on the backend of the app, a choice the window begins
// reaches the shell with its number and the title of the saga; the folder the shell names is checked and kept, and the
// link and the tree of the vault are read through the window; a folder that is not one, a number spent and a number never
// declared change nothing; the log of the run names neither the vault, nor a note, nor a number.
func TestTheBackendLinksASagaWithTheFolderTheShellPicked(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: time.Minute}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the code = %d", response.StatusCode)
	}
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	const marker = "Хранилище-метка-пути"
	vault := filepath.Join(t.TempDir(), marker)
	for name, text := range map[string]string{
		"Книга 1/_book.md":            "---\nbook: Застава\n---\n",
		"Книга 1/01 Застава.md":       "Ольга вышла к заставе на рассвете.",
		"Книга 1/02 Переправа.md":     "Мирон ждал у переправы.\n\n## Интерлюдия\n\nОльга смотрела с башни.",
		"Персонажи/Заметка-метка.md":  "Мирон — старший брат.",
		"Книга 1/Ссылка-метка.md.txt": "не заметка",
	} {
		full := filepath.Join(vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notFolder := filepath.Join(vault, "Книга 1", "01 Застава.md")
	// OB0.4: a note that does not read — no right to read it — fails its reading, and the log names it nowhere.
	closed := filepath.Join(vault, "Книга 1", "03 Закрытая-метка.md")
	if err := os.WriteFile(closed, []byte("Закрыто."), 0o000); err != nil {
		t.Fatal(err)
	}
	unreadable := os.Geteuid() != 0

	read := func() map[string]any {
		t.Helper()
		response, body := window.do(http.MethodGet, "/api/v1/projects/"+project.ID+"/obsidian", "", nil)
		var state map[string]any
		if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil {
			t.Fatalf("getObsidianVault = %d %s", response.StatusCode, body)
		}
		return state
	}
	if state := read(); state["linked"] != false {
		t.Fatalf("before a choice = %v", state)
	}
	if response, body := window.do(http.MethodGet, "/api/v1/projects/"+project.ID+"/obsidian/tree", "", nil); response.StatusCode != http.StatusConflict ||
		!strings.Contains(string(body), "obsidian.not_linked") {
		t.Fatalf("a look without a vault = %d %s", response.StatusCode, body)
	}

	spent := process.choose(window, project.ID)
	if outcome := process.picked(spent, notFolder); outcome != "not_folder" {
		t.Fatalf("a file as the vault = %s", outcome)
	}
	if state := read(); state["linked"] != false || state["refused"] != "not_folder" {
		t.Fatalf("after a refusal = %v", state)
	}
	if outcome := process.picked(spent, vault); outcome != "stale" {
		t.Fatalf("a spent number = %s", outcome)
	}
	if outcome := process.picked(strings.Repeat("A", 43), vault); outcome != "stale" {
		t.Fatalf("a number never declared = %s", outcome)
	}
	request := process.choose(window, project.ID)
	if state := read(); state["choosing"] != true {
		t.Fatalf("while the choice waits = %v", state)
	}
	if outcome := process.picked(request, vault); outcome != "linked" {
		t.Fatalf("the vault = %s", outcome)
	}
	state := read()
	if state["linked"] != true || state["path"] != vault || state["name"] != marker || state["availability"] != "available" {
		t.Fatalf("the link = %v", state)
	}
	response, body = window.do(http.MethodGet, "/api/v1/projects/"+project.ID+"/obsidian/tree", "", nil)
	if response.StatusCode != http.StatusOK || strings.Contains(string(body), vault) ||
		!strings.Contains(string(body), `"path":"Книга 1"`) || !strings.Contains(string(body), `"hint":{"kind":"book"}`) {
		t.Fatalf("the tree = %d %s", response.StatusCode, body)
	}
	// OB4.1–OB4.4: the preview reads the chosen folders and writes nothing into the saga.
	connection := database(t, data)
	counts := func() string {
		t.Helper()
		var books, chapters, records, jobs, queues, uploads int
		if err := connection.QueryRow(context.Background(), `SELECT
			(SELECT count(*) FROM books WHERE project_id = $1), (SELECT count(*) FROM chapters c JOIN books b ON b.id = c.book_id WHERE b.project_id = $1),
			(SELECT count(*) FROM author_materials WHERE project_id = $1), (SELECT count(*) FROM jobs WHERE project_id = $1),
			(SELECT count(*) FROM import_queues WHERE project_id = $1), (SELECT count(*) FROM import_uploads)`, project.ID).
			Scan(&books, &chapters, &records, &jobs, &queues, &uploads); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(books, chapters, records, jobs, queues, uploads)
	}
	before := counts()
	response, body = window.do(http.MethodPost, "/api/v1/projects/"+project.ID+"/obsidian/preview", window.csrf(), map[string]any{
		"books": []string{"Книга 1"}, "materials": []map[string]string{{"path": "Персонажи", "kind": "character"}},
	})
	var preview struct {
		Books []struct {
			Title    string `json:"title"`
			Chapters []struct {
				Title string `json:"title"`
			} `json:"chapters"`
			Warnings []struct {
				Code  string   `json:"code"`
				Notes []string `json:"notes"`
			} `json:"warnings"`
			Skipped []struct {
				Reason string `json:"reason"`
				Count  int    `json:"count"`
			} `json:"skipped"`
			Fingerprint string `json:"fingerprint"`
		} `json:"books"`
		Materials struct {
			Records []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"records"`
		} `json:"materials"`
		Reading struct {
			NewChapters int    `json:"newChapters"`
			Helper      string `json:"helper"`
		} `json:"reading"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &preview) != nil || len(preview.Books) != 1 || strings.Contains(string(body), vault) {
		t.Fatalf("previewObsidianImport = %d %s", response.StatusCode, body)
	}
	var previewed []string
	for _, chapter := range preview.Books[0].Chapters {
		previewed = append(previewed, chapter.Title)
	}
	if preview.Books[0].Title != "Застава" || !slices.Equal(previewed, []string{"Застава", "Переправа", "Интерлюдия"}) ||
		len(preview.Books[0].Warnings) != 1 || preview.Books[0].Warnings[0].Code != "chapters_differ" ||
		len(preview.Materials.Records) != 1 || preview.Materials.Records[0].State != "new" ||
		preview.Reading.NewChapters != 3 || preview.Reading.Helper != "none" {
		t.Fatalf("the preview = %s", body)
	}
	if after := counts(); after != before {
		t.Fatalf("the preview wrote: %s, then %s", before, after)
	}
	if unreadable && !slices.ContainsFunc(preview.Books[0].Skipped, func(skip struct {
		Reason string `json:"reason"`
		Count  int    `json:"count"`
	}) bool {
		return skip.Reason == "unreadable" && skip.Count == 1
	}) {
		t.Fatalf("the note that does not read = %s", body)
	}

	// The chapters of the preview are the chapters the import makes of the same glued text.
	var notes []obsidian.Note
	for _, name := range []string{"Книга 1/_book.md", "Книга 1/01 Застава.md", "Книга 1/02 Переправа.md"} {
		raw, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		note, skip := obsidian.ReadNote(name, raw)
		if skip != "" {
			t.Fatal(skip)
		}
		notes = append(notes, note)
	}
	glued := obsidian.Glue("Книга 1", "Книга 1", notes)
	if glued.Fingerprint != preview.Books[0].Fingerprint {
		t.Fatalf("the glued text is not the one of the preview")
	}
	window.importFileAs(project.ID, glued.FileName, glued.Text, "import-of-the-glued-book")
	deadline := time.Now().Add(90 * time.Second)
	for {
		var list struct {
			Data []struct {
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"data"`
		}
		window.read("/api/v1/projects/"+project.ID+"/jobs", &list)
		imported := slices.ContainsFunc(list.Data, func(job struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
		}) bool {
			return job.Kind == "import" && job.Status == "succeeded"
		})
		if imported {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the import did not end: %+v", list.Data)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var books struct {
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	window.read("/api/v1/projects/"+project.ID+"/books", &books)
	var chapters struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	window.read("/api/v1/books/"+books.Data[0].ID+"/chapters", &chapters)
	var imported []string
	for _, chapter := range chapters.Data {
		imported = append(imported, chapter.Title)
	}
	if books.Data[0].Title != preview.Books[0].Title || !slices.Equal(imported, previewed) {
		t.Fatalf("the import made %q %q, the preview said %q %q", books.Data[0].Title, imported, preview.Books[0].Title, previewed)
	}

	if response, body := window.do(http.MethodDelete, "/api/v1/projects/"+project.ID+"/obsidian", window.csrf(), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("unlinkObsidianVault = %d %s", response.StatusCode, body)
	}
	if state := read(); state["linked"] != false {
		t.Fatalf("after the unlink = %v", state)
	}

	logs, err := os.ReadDir(filepath.Join(data, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var journal strings.Builder
	for _, entry := range logs {
		raw, _ := os.ReadFile(filepath.Join(data, "logs", entry.Name()))
		journal.Write(raw)
	}
	for _, secret := range []string{marker, "Заметка-метка", "Ссылка-метка", "Закрытая-метка", request, spent} {
		if strings.Contains(journal.String(), secret) {
			t.Fatalf("the log of the run names %q", secret)
		}
	}
	if !strings.Contains(journal.String(), `"obsidian_folder_refused","reason":"not_folder"`) {
		t.Fatalf("the log keeps no refusal of a folder:\n%s", journal.String())
	}
}
