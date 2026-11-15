// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// update answers an update by the saga it is asked of and keeps what it was asked.
type update struct{}

var askedUpdate service.UpdateInput

func (update) Execute(_ context.Context, input service.UpdateInput) (service.Updated, error) {
	askedUpdate = input
	switch input.ProjectID {
	case "busy":
		return service.Updated{}, service.QueueBusy{Queue: service.QueueRef{ID: "q-1", Status: "draft"}}
	case "changed":
		return service.Updated{}, service.FolderChanged{Folders: []string{"Книга 1"}}
	case "missing":
		return service.Updated{}, service.FolderMissing{Folder: "Книга 1"}
	case "unlinked":
		return service.Updated{}, service.ErrBookNotLinked
	case "unread":
		return service.Updated{}, service.NotesUnread{Unread: domain.Skipped{domain.SkipCloudOnly: 2}}
	case "same":
		return service.Updated{}, service.ErrNothingToApply
	case "large":
		return service.Updated{}, service.ImportRefused{Err: library.ErrImportFileTooLarge}
	case "slow":
		return service.Updated{}, service.ErrSlow
	}
	job := jobs.Job{
		ID: "11111111-1111-4111-8111-111111111111", Kind: jobs.KindImport, Status: jobs.StatusQueued, ProjectID: input.ProjectID,
		Subject:   jobs.ImportSubject{Type: jobs.KindImport, ID: input.ProjectID, ProjectID: input.ProjectID, BookID: input.BookID, Preview: !input.Apply},
		CreatedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
	}
	if input.Apply {
		return service.Updated{Job: job}, nil
	}
	return service.Updated{Job: job, Fingerprint: fingerprint, Book: &service.BookPreview{
		Folder: "Книга 1", Title: "Застава", Skipped: domain.Skipped{domain.SkipCloudOnly: 1}, Fingerprint: fingerprint,
		Chapters: []service.VolumeChapter{{Title: "Пролог", Characters: 40}}, State: service.FolderState{Linked: true, BookID: "b-1", Changed: true},
	}}, nil
}

// bookFolders link and unlink the books of a test.
type bookFolders struct{}

var askedFolder service.BookFolderInput

func (bookFolders) Execute(_ context.Context, input service.BookFolderInput) error {
	askedFolder = input
	switch input.ProjectID {
	case "linked-other":
		return service.FolderLinked{Folder: "Книга 1", BookID: "b-2"}
	case "overlap":
		return domain.Overlap{Outer: "Сага", Inner: "Сага/Книга 1"}
	case "missing":
		return service.FolderMissing{Folder: "Книга 9"}
	case "path":
		return domain.ErrPath
	}
	return nil
}

func updateOf(project, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	return askWith(http.MethodPost, "/projects/"+project+"/obsidian/books/b-1/update", body, func(request *http.Request) {
		request.Header.Set("Idempotency-Key", "update-key-0001")
		if change != nil {
			change(request)
		}
	})
}

// specs/2026-11-14-desk-obsidian-books.md OK2.1, OK2.2, OK2.4, OK2.5: updateBookFromObsidian takes its mode with one
// Idempotency-Key and the proof of the session; it answers the start of the import Job as the import of a book does — a
// preview with the fingerprint and the report of the folder beside it —, the refusals of the bridge with what the page needs
// and those of the core as the core says them.
func TestTheUpdateOfABookFromItsFolder(t *testing.T) {
	recorder := updateOf("saga", `{"mode":"preview"}`, nil)
	var preview struct {
		JobID       string               `json:"jobId"`
		Links       struct{ Job string } `json:"links"`
		Fingerprint string               `json:"fingerprint"`
		Book        struct {
			Folder  string           `json:"folder"`
			Skipped []map[string]any `json:"skipped"`
			State   struct {
				Kind    string `json:"kind"`
				Changed bool   `json:"changed"`
			} `json:"state"`
		} `json:"book"`
	}
	if recorder.Code != http.StatusAccepted || json.Unmarshal(recorder.Body.Bytes(), &preview) != nil || preview.JobID == "" ||
		preview.Links.Job != "/api/v1/jobs/"+preview.JobID || recorder.Header().Get("Location") != preview.Links.Job ||
		preview.Fingerprint != fingerprint || preview.Book.Folder != "Книга 1" || preview.Book.State.Kind != "linked" ||
		!preview.Book.State.Changed || len(preview.Book.Skipped) != 1 {
		t.Fatalf("the preview = %d %s", recorder.Code, recorder.Body)
	}
	if askedUpdate.Apply || askedUpdate.BookID != "b-1" || askedUpdate.Key != "update-key-0001" || askedUpdate.Path != "/projects/saga/obsidian/books/b-1/update" {
		t.Fatalf("the use case was asked %+v", askedUpdate)
	}
	recorder = updateOf("saga", `{"mode":"apply","expectedFingerprint":"`+fingerprint+`","confirmUnread":true}`, nil)
	if recorder.Code != http.StatusAccepted || strings.Contains(recorder.Body.String(), `"fingerprint"`) || !askedUpdate.Apply ||
		askedUpdate.Expected != fingerprint || !askedUpdate.ConfirmUnread {
		t.Fatalf("the application = %d %s, asked %+v", recorder.Code, recorder.Body, askedUpdate)
	}
	for name, body := range map[string]string{
		"no mode":                    `{}`,
		"an apply without its print": `{"mode":"apply"}`,
		"a preview with a print":     `{"mode":"preview","expectedFingerprint":"` + fingerprint + `"}`,
		"a preview with a word":      `{"mode":"preview","confirmUnread":true}`,
		"an unknown field":           `{"mode":"preview","bookId":"b-2"}`,
	} {
		if recorder := updateOf("saga", body, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body)
		}
	}
	if recorder := updateOf("saga", `{"mode":"preview"}`, func(request *http.Request) { request.Header.Del("Idempotency-Key") }); recorder.Code != http.StatusBadRequest {
		t.Errorf("no key = %d", recorder.Code)
	}
	for project, want := range map[string]struct {
		status int
		code   string
		field  string
	}{
		"busy":     {http.StatusConflict, domain.CodeQueueBusy, `"queue":{"id":"q-1","status":"draft"}`},
		"changed":  {http.StatusConflict, domain.CodeFolderChanged, `"folders":["Книга 1"]`},
		"missing":  {http.StatusConflict, domain.CodeFolderMissing, `"folders":["Книга 1"]`},
		"unlinked": {http.StatusConflict, domain.CodeBookNotLinked, ""},
		"unread":   {http.StatusConflict, domain.CodeNotesUnread, `"unread":[{"reason":"cloud_only","count":2}]`},
		"same":     {http.StatusConflict, domain.CodeNothingToApply, ""},
		"large":    {http.StatusRequestEntityTooLarge, "import.file_too_large", ""},
		"slow":     {http.StatusServiceUnavailable, domain.CodeSlow, ""},
	} {
		recorder := updateOf(project, `{"mode":"apply","expectedFingerprint":"`+fingerprint+`"}`, nil)
		if recorder.Code != want.status || !strings.Contains(recorder.Body.String(), `"code":"`+want.code+`"`) ||
			!strings.Contains(recorder.Body.String(), want.field) {
			t.Errorf("%s = %d %s", project, recorder.Code, recorder.Body)
		}
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.3: linkBookToObsidianFolder takes the folder in its body and answers nothing;
// unlinkBookFromObsidianFolder takes nothing; their refusals carry the folders and the book that holds one.
func TestTheLinkOfABookWithAFolder(t *testing.T) {
	if recorder := askWith(http.MethodPut, "/projects/saga/obsidian/books/b-1", `{"folder":"Книга 1"}`, nil); recorder.Code != http.StatusNoContent ||
		askedFolder.Folder != "Книга 1" || askedFolder.BookID != "b-1" {
		t.Fatalf("a link = %d %s, asked %+v", recorder.Code, recorder.Body, askedFolder)
	}
	if recorder := askWith(http.MethodPut, "/projects/saga/obsidian/books/b-1", `{"folder":""}`, nil); recorder.Code != http.StatusNoContent || askedFolder.Folder != "" {
		t.Fatalf("a link with the root of the vault = %d %s", recorder.Code, recorder.Body)
	}
	for name, body := range map[string]string{"no folder": `{}`, "an unknown field": `{"folder":"Книга 1","exclude":[]}`} {
		if recorder := askWith(http.MethodPut, "/projects/saga/obsidian/books/b-1", body, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, recorder.Code)
		}
	}
	for project, want := range map[string]struct {
		status int
		code   string
		field  string
	}{
		"linked-other": {http.StatusConflict, domain.CodeFolderLinked, `"bookId":"b-2"`},
		"overlap":      {http.StatusUnprocessableEntity, domain.CodeFoldersOverlap, `"folders":["Сага","Сага/Книга 1"]`},
		"missing":      {http.StatusConflict, domain.CodeFolderMissing, `"folders":["Книга 9"]`},
		"path":         {http.StatusBadRequest, "request.validation", ""},
	} {
		recorder := askWith(http.MethodPut, "/projects/"+project+"/obsidian/books/b-1", `{"folder":"Книга 1"}`, nil)
		if recorder.Code != want.status || !strings.Contains(recorder.Body.String(), `"code":"`+want.code+`"`) ||
			!strings.Contains(recorder.Body.String(), want.field) {
			t.Errorf("%s = %d %s", project, recorder.Code, recorder.Body)
		}
	}
	if recorder := askWith(http.MethodDelete, "/projects/saga/obsidian/books/b-1", "", nil); recorder.Code != http.StatusNoContent || askedFolder.BookID != "b-1" {
		t.Fatalf("an unlink = %d %s", recorder.Code, recorder.Body)
	}
	if recorder := askWith(http.MethodDelete, "/projects/saga/obsidian/books/b-1", "", func(request *http.Request) {
		request.Header.Set("Origin", "https://example.test")
	}); recorder.Code != http.StatusForbidden {
		t.Fatalf("an unlink from another Origin = %d", recorder.Code)
	}
}

// vaultWithBooks is a saga whose vault holds the folders of two books, a third book linked anew.
type vaultWithBooks struct{}

func (vaultWithBooks) Execute(context.Context, string, string) (service.VaultState, error) {
	loaded := time.Date(2026, 11, 14, 9, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	return service.VaultState{
		Linked: true, Path: "/vaults/Северный предел", Name: "Северный предел",
		Books: []service.VaultBook{
			{BookID: "b-1", Title: "Застава", Folder: "Книга 1", LoadedAt: &loaded, Present: new(true), Choice: service.BookChoice{
				Excluded: []string{"Книга 1/Черновики", "Книга 1/Пролог.md"}, Nested: []string{"Книга 1/Персонажи"},
			}},
			{BookID: "b-2", Title: "Переправа", Folder: "Книга 2", Present: new(false)},
		},
		Relink: []service.VaultBook{{BookID: "b-3", Title: "Застава (копия)", Folder: "Книга 1"}},
	}, nil
}

// specs/2026-11-14-desk-obsidian-books.md OK0.4: the tree names the book that holds a folder; a folder whose volume is in
// work of a queue has no book yet — the tree names none (its state is the preview's), not a book without an id.
func TestTheTreeNamesOnlyTheBooksThatHoldAFolder(t *testing.T) {
	tree := ask(http.MethodGet, "/projects/queued/obsidian/tree", nil)
	var body struct {
		Folders []struct {
			Path string          `json:"path"`
			Book json.RawMessage `json:"book"`
			Hint struct {
				Kind string `json:"kind"`
			} `json:"hint"`
		} `json:"folders"`
	}
	if tree.Code != http.StatusOK || json.Unmarshal(tree.Body.Bytes(), &body) != nil || len(body.Folders) != 2 {
		t.Fatalf("the tree = %d %s", tree.Code, tree.Body)
	}
	if string(body.Folders[0].Book) != `{"bookId":"b-1","title":"Застава"}` {
		t.Fatalf("the folder a book holds = %s", tree.Body)
	}
	if body.Folders[1].Book != nil || body.Folders[1].Hint.Kind != "book" {
		t.Fatalf("the folder whose volume is in work = %s", tree.Body)
	}
}

// specs/2026-11-14-desk-obsidian-books.md OK2.6: getObsidianVault names the linked books with their folders, when they were
// last loaded, whether the folder is there — unknown when the check did not say —, and the books to link anew; with the
// composition each link keeps — empty lists for a book linked without a load (specs/2026-11-15-desk-obsidian-web.md OW2.5).
func TestTheVaultNamesItsBooks(t *testing.T) {
	router := chi.NewRouter()
	Handlers{
		Read: vaultWithBooks{}, Begin: begin{}, Unlink: unlink{}, Scan: scan{}, Preview: preview{}, Load: load{}, Update: update{},
		LinkBook: bookFolders{}, UnlinkBook: bookFolders{}, Materials: materials{}, Users: sessions{}, AllowedOrigins: []string{origin},
	}.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/projects/saga/obsidian", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "author"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, want := range []string{
		`"books":[{"bookId":"b-1","title":"Застава","folder":"Книга 1","loadedAt":"2026-11-14T06:00:00Z","presence":"present",` +
			`"excluded":["Книга 1/Черновики","Книга 1/Пролог.md"],"nested":["Книга 1/Персонажи"]}`,
		`{"bookId":"b-2","title":"Переправа","folder":"Книга 2","loadedAt":null,"presence":"missing","excluded":[],"nested":[]}`,
		`"relink":[{"bookId":"b-3","title":"Застава (копия)","folder":"Книга 1","loadedAt":null,"presence":"unknown","excluded":[],"nested":[]}]`,
	} {
		if recorder.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("the vault = %d %s, want %s", recorder.Code, body, want)
		}
	}
}
