// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"

	domain "canonkeeper/backend/internal/domain/sagaarchive"
)

// sagaFile writes a made-up saga of books × chapters × paragraphs: a word of its own stands in one paragraph of it.
func sagaFile(t *testing.T, books, chapters, paragraphs int, word string) []byte {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id := func(kind, n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", kind, n) }
	var out bytes.Buffer
	writer := domain.NewWriter(&out)
	must(writer.Saga(domain.Saga{Name: "Северный предел", BookKeyNumbers: books}))
	shelf, err := writer.Books()
	must(err)
	for book := 1; book <= books; book++ {
		must(shelf.Add(domain.Book{Ref: id(1, book), KeyNumber: book, Order: book, Title: fmt.Sprintf("Том %d", book)}))
	}
	must(shelf.Close())
	lines, err := writer.Chapters()
	must(err)
	for book := 1; book <= books; book++ {
		for chapter := 1; chapter <= chapters; chapter++ {
			order := chapter - 1
			filed := domain.Chapter{Ref: id(2, book*1000+chapter), Book: id(1, book), Order: chapter, PublishedOrder: &order,
				Title: fmt.Sprintf("Застава %d", chapter)}
			for paragraph := 1; paragraph <= paragraphs; paragraph++ {
				text := fmt.Sprintf("Мара шла к заставе %d тома %d, и *ветер* нёс снег над перевалом %d.", chapter, book, paragraph)
				if book == books/2+1 && chapter == chapters/2 && paragraph == paragraphs/2 {
					text = "Над перевалом показался " + word + "."
				}
				filed.Paragraphs = append(filed.Paragraphs, domain.Paragraph{
					ID: id(3, (book*1000+chapter)*1000+paragraph), Order: paragraph, Kind: "paragraph", Markdown: text,
				})
			}
			must(lines.Add(filed))
		}
	}
	must(lines.Close())
	must(writer.Close(domain.Manifest{CreatedBy: "canonkeeper.ru", CreatedAt: time.Now(), Saga: domain.ManifestSaga{Name: "Северный предел"}}))
	return out.Bytes()
}

// specs/2026-11-13-desk-sagafile.md SF3.3, SF3.12, SF3.13: the window opens a saga file of 13 books of 50 chapters of 150
// paragraphs through its HTTP; once the text index of the app ran, the contents and the search answer of the new saga.
func TestTheWindowOpensASagaFile(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, nil)
	browser := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: desktoptest.Waited(5 * time.Minute)}}
	if response := browser.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the code = %d", response.StatusCode)
	}
	const word = "Ойвинградъ"
	file := sagaFile(t, 13, 50, 150, word)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "Северный предел.cksaga")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(file)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, process.origin()+"/api/v1/saga-archives", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Origin", process.origin())
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.Header.Set("Idempotency-Key", "open-the-saga-of-the-test")
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	started := time.Now()
	response, err := browser.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	took := time.Since(started)
	t.Logf("the open of a saga of 13 × 50 × 150 paragraphs, a file of %d bytes: %s", len(file), took)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("the open = %d %s", response.StatusCode, answer)
	}
	if took > time.Minute {
		t.Errorf("the open took %s, over the minute of SF3.13", took)
	}
	var opened struct {
		ProjectID string `json:"projectId"`
		Report    struct {
			Books, Chapters int
		} `json:"report"`
	}
	if err := json.Unmarshal(answer, &opened); err != nil || opened.Report.Books != 13 || opened.Report.Chapters != 650 {
		t.Fatalf("the answer = %s, %v", answer, err)
	}
	deadline := desktoptest.Deadline(5 * time.Minute)
	for {
		response, contents := browser.do(http.MethodGet, "/api/v1/projects/"+opened.ProjectID+"/saga/contents?sourceView=published&q="+url.QueryEscape("Застава"), "", nil)
		if response.StatusCode == http.StatusOK && strings.Contains(string(contents), "Застава 25") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the contents of the new saga after the index = %d %s", response.StatusCode, contents)
		}
		time.Sleep(time.Second)
	}
	t.Logf("the text index of the saga ended %s after the open", time.Since(started))
	response, found := browser.do(http.MethodPost, "/api/v1/projects/"+opened.ProjectID+"/search", browser.csrf(), map[string]any{
		"query": word, "scope": "all", "sourceView": "published", "limit": 5,
	})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(found), word) {
		t.Fatalf("the search of the new saga = %d %s", response.StatusCode, found)
	}
}
