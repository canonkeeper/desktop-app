// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// load answers a load by the saga it is asked of and keeps what it was asked.
type load struct{}

var asked service.LoadInput

func (load) Execute(_ context.Context, input service.LoadInput) (service.Loaded, error) {
	asked = input
	switch input.ProjectID {
	case "busy":
		return service.Loaded{}, service.QueueBusy{Queue: service.QueueRef{ID: "q-1", Status: "running"}}
	case "changed":
		return service.Loaded{}, service.FolderChanged{Folders: []string{"Книга 1"}}
	case "failed":
		return service.Loaded{}, service.LoadFailed{Code: "auth.session_required"}
	case "interrupted":
		return service.Loaded{}, service.ErrLoadInterrupted
	case "reused":
		return service.Loaded{}, service.ErrReplayConflict
	case "slow":
		return service.Loaded{}, service.ErrSlow
	}
	return service.Loaded{
		Queue: &service.QueueRef{ID: "q-1", Status: "running"},
		Folders: []service.FolderOutcome{
			{Folder: "Книга 1", Outcome: service.OutcomeQueued, QueueID: "q-1"},
			{Folder: "Книга 2", Outcome: service.OutcomeLinked, BookID: "b-2", Title: "Переправа"},
			{Folder: "Книга 3", Outcome: service.OutcomeRefused, Refusal: "import.too_many_chapters"},
		},
	}, nil
}

const fingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func loadBody(books ...string) string {
	var entries []string
	for _, book := range books {
		entries = append(entries, `{"folder":"`+book+`","fingerprint":"`+fingerprint+`"}`)
	}
	return `{"books":[` + strings.Join(entries, ",") + `],"materials":[{"path":"Персонажи","kind":"character"}],"exclude":["Книга 1/Черновики"]}`
}

func loadOf(project, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	return askWith(http.MethodPost, "/projects/"+project+"/obsidian/books", body, func(request *http.Request) {
		request.Header.Set("Idempotency-Key", "load-key-0001")
		if change != nil {
			change(request)
		}
	})
}

// specs/2026-11-14-desk-obsidian-books.md OK1.1, OK1.2, OK1.6, Data: importObsidianBooks takes the choice and the
// fingerprints in its body with one Idempotency-Key and the proof of the session, answers the outcome of each folder and
// says each refusal of the load with what the page needs.
func TestTheLoadOfTheBooksOfTheFolders(t *testing.T) {
	recorder := loadOf("saga", loadBody("Книга 1", "Книга 2", "Книга 3"), nil)
	var body struct {
		Queue   *struct{ ID, Status string } `json:"queue"`
		Folders []map[string]string          `json:"folders"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Queue == nil || body.Queue.ID != "q-1" ||
		len(body.Folders) != 3 || body.Folders[0]["outcome"] != "queued" || body.Folders[1]["bookId"] != "b-2" ||
		body.Folders[2]["refusal"] != "import.too_many_chapters" {
		t.Fatalf("the load = %d %s", recorder.Code, recorder.Body)
	}
	if asked.Key != "load-key-0001" || asked.Fingerprints["Книга 2"] != fingerprint || len(asked.Choice.Materials) != 1 ||
		asked.Choice.Exclude[0] != "Книга 1/Черновики" || asked.Method != http.MethodPost || asked.Path != "/projects/saga/obsidian/books" {
		t.Fatalf("the use case was asked %+v", asked)
	}
	for name, change := range map[string]func(*http.Request){
		"no key":         func(request *http.Request) { request.Header.Del("Idempotency-Key") },
		"a short key":    func(request *http.Request) { request.Header.Set("Idempotency-Key", "short") },
		"two keys":       func(request *http.Request) { request.Header.Add("Idempotency-Key", "load-key-0002") },
		"a query":        func(request *http.Request) { request.URL.RawQuery = "x=1" },
		"no write token": func(request *http.Request) { request.Header.Del("X-CSRF-Token") },
	} {
		if recorder := loadOf("saga", loadBody("Книга 1"), change); recorder.Code < 400 || recorder.Code >= 500 {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body)
		}
	}
	for name, body := range map[string]string{
		"no book":           `{"books":[]}`,
		"a bad fingerprint": `{"books":[{"folder":"Книга 1","fingerprint":"ABC"}]}`,
		"a folder twice":    `{"books":[{"folder":"Книга 1","fingerprint":"` + fingerprint + `"},{"folder":"Книга 1","fingerprint":"` + fingerprint + `"}]}`,
		"an unknown field":  `{"books":[{"folder":"Книга 1","fingerprint":"` + fingerprint + `","more":1}]}`,
		"a second document": loadBody("Книга 1") + "{}",
	} {
		if recorder := loadOf("saga", body, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body)
		}
	}
	for project, want := range map[string]struct {
		status int
		code   string
		field  string
	}{
		"busy":        {http.StatusConflict, domain.CodeQueueBusy, `"queue":{"id":"q-1","status":"running"}`},
		"changed":     {http.StatusConflict, domain.CodeFolderChanged, `"folders":["Книга 1"]`},
		"failed":      {http.StatusUnauthorized, "auth.session_required", ""},
		"interrupted": {http.StatusConflict, domain.CodeLoadInterrupted, ""},
		"reused":      {http.StatusConflict, "idempotency.conflict", ""},
		"slow":        {http.StatusServiceUnavailable, domain.CodeSlow, ""},
	} {
		recorder := loadOf(project, loadBody("Книга 1"), nil)
		if recorder.Code != want.status || !strings.Contains(recorder.Body.String(), `"code":"`+want.code+`"`) ||
			!strings.Contains(recorder.Body.String(), want.field) {
			t.Errorf("%s = %d %s", project, recorder.Code, recorder.Body)
		}
	}
}
