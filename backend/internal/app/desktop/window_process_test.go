// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// specs/2026-11-12-desk-window.md WN1.1: the built backend of the app answers «кто автор» of its window with the local
// author in the form User — verified, nothing to accept — and a request without the session of the window with the 401
// of the site.
func TestTheWindowOfTheAppKnowsItsAuthor(t *testing.T) {
	binary, resources := desktopBinary(t)
	process := startBackend(t, binary, resources, filepath.Join(t.TempDir(), "data"), nil)
	stranger := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response, body := stranger.do(http.MethodGet, "/api/v1/auth/session", "", nil); response.StatusCode != http.StatusUnauthorized ||
		!json.Valid(body) || !containsCode(body, "auth.session_required") {
		t.Fatalf("getCurrentUser without the session = %d %s", response.StatusCode, body)
	}

	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	response, body := window.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	var author map[string]any
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &author) != nil {
		t.Fatalf("getCurrentUser = %d %s", response.StatusCode, body)
	}
	if author["email"] != "local@desktop.invalid" || author["displayName"] != "Автор" || author["emailVerified"] != true ||
		author["documentsAccepted"] != true || author["id"] == "" || author["createdAt"] == "" || len(author) != 7 {
		t.Fatalf("the author of the window = %v", author)
	}
}

func containsCode(body []byte, code string) bool {
	var problem struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &problem) == nil && problem.Code == code
}

// A book of the invented saga «Северный предел»: two chapters by their headings of the first level.
const northernBook = "# Глава 1. Сигнальные огни\n\nВера поднялась на стену заставы, когда над перевалом погасли сигнальные огни.\n\n" +
	"# Глава 2. Перевал\n\nДозорные спустились к перевалу на рассвете и нашли следы чужого отряда.\n"

// importFile sends a file of a book into the saga as the window does, multipart with its key of the request.
func (browser *client) importFile(projectID, name, text string) {
	browser.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", name)
	if err != nil {
		browser.t.Fatal(err)
	}
	_, _ = io.WriteString(file, text)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, browser.origin+"/api/v1/projects/"+projectID+"/imports", &body)
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.Header.Set("Idempotency-Key", "import-of-the-northern-book")
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	response, err := browser.http.Do(request)
	if err != nil {
		browser.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(response.Body)
		browser.t.Fatalf("importBookFile = %d %s", response.StatusCode, raw)
	}
}

// read is a GET of the window that must answer 200 and decodes its answer.
func (browser *client) read(path string, target any) []byte {
	browser.t.Helper()
	response, body := browser.do(http.MethodGet, path, "", nil)
	if response.StatusCode != http.StatusOK || (target != nil && json.Unmarshal(body, target) != nil) {
		browser.t.Fatalf("GET %s = %d %s", path, response.StatusCode, body)
	}
	return body
}

// specs/2026-11-12-desk-window.md WN3.1, WN3.4: the backend of the app without a model imports a book and indexes its
// text, queues no cards Job and fails none; the status says the reading waits for the helper; the chapters are read and
// found at once.
func TestTheAppWithoutItsHelperReadsAndFindsTheBooksAndQueuesNoCards(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
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

	deadline := time.Now().Add(90 * time.Second)
	for {
		var list struct {
			Data []struct {
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"data"`
		}
		window.read("/api/v1/projects/"+project.ID+"/jobs", &list)
		working, indexed := false, 0
		for _, job := range list.Data {
			working = working || job.Status == "queued" || job.Status == "running"
			if job.Kind == "canon_text_index" && job.Status == "succeeded" {
				indexed++
			}
		}
		if !working && indexed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the import and the text index did not end: %+v", list.Data)
		}
		time.Sleep(200 * time.Millisecond)
	}

	connection := database(t, data)
	var cards, failed int
	if err := connection.QueryRow(context.Background(), `SELECT
		count(*) FILTER (WHERE kind = 'chapter_cards'), count(*) FILTER (WHERE status = 'failed')
		FROM jobs WHERE project_id = $1`, project.ID).Scan(&cards, &failed); err != nil {
		t.Fatal(err)
	}
	if cards != 0 || failed != 0 {
		t.Fatalf("cards jobs = %d, failed jobs = %d; want none", cards, failed)
	}

	var status struct {
		Preparation *struct {
			Waiting          string `json:"waiting"`
			ChaptersRead     int    `json:"chaptersRead"`
			ChaptersTotal    int    `json:"chaptersTotal"`
			RemainingSeconds *int   `json:"remainingSeconds"`
			LetterPromised   bool   `json:"letterPromised"`
		} `json:"preparation"`
	}
	window.read("/api/v1/projects/"+project.ID+"/indexing-status", &status)
	if status.Preparation == nil || status.Preparation.Waiting != "helper" || status.Preparation.ChaptersRead != 0 ||
		status.Preparation.ChaptersTotal != 2 || status.Preparation.RemainingSeconds != nil || status.Preparation.LetterPromised {
		t.Fatalf("the preparation = %+v", status.Preparation)
	}

	var books struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	window.read("/api/v1/projects/"+project.ID+"/books", &books)
	if len(books.Data) != 1 {
		t.Fatalf("books = %+v", books.Data)
	}
	var chapters struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	window.read("/api/v1/books/"+books.Data[0].ID+"/chapters", &chapters)
	if len(chapters.Data) != 2 {
		t.Fatalf("chapters = %+v", chapters.Data)
	}
	if chapter := window.read("/api/v1/chapters/"+chapters.Data[0].ID, nil); !strings.Contains(string(chapter), "сигнальные огни") {
		t.Fatalf("the first chapter = %s", chapter)
	}
	response, body = window.do(http.MethodPost, "/api/v1/projects/"+project.ID+"/search", window.csrf(), map[string]string{
		"query": "перевалу", "scope": "all", "sourceView": "published",
	})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Дозорные") {
		t.Fatalf("searchProject = %d %s", response.StatusCode, body)
	}
}
