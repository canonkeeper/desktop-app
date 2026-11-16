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
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

const origin = "http://127.0.0.1:24680"

// sessions know one author by the cookie «author» and his token «token».
type sessions struct{}

func (sessions) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	if cookie != "author" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "author"}, nil
}

func (sessions) AuthenticateWrite(ctx context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	user, err := sessions{}.CurrentUser(ctx, cookie)
	if err != nil {
		return auth.User{}, auth.SessionWriteProof{}, err
	}
	if csrf != "token" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return user, auth.SessionWriteProof{}, nil
}

// bridge answers each operation by the saga it is asked of.
type bridge struct{}

func (bridge) Execute(_ context.Context, _, project string) (service.VaultState, error) {
	if project == "linked" {
		return service.VaultState{Linked: true, Path: "/home/author/Северный предел", Name: "Северный предел", Available: new(true)}, nil
	}
	return service.VaultState{Refused: domain.ReasonNotFolder}, nil
}

type begin struct{}

func (begin) Execute(_ context.Context, _, project string) (string, error) {
	if project == "headless" {
		return "", service.ErrChoiceUnavailable
	}
	return strings.Repeat("A", 43), nil
}

type unlink struct{}

func (unlink) Execute(context.Context, string, string) error { return nil }

type scan struct{}

func (scan) Execute(_ context.Context, input service.ScanInput) (service.Scanned, error) {
	switch input.ProjectID {
	case "busy":
		return service.Scanned{}, service.Busy{RetryAfter: 1500 * time.Millisecond}
	case "slow":
		return service.Scanned{}, service.ErrSlow
	case "gone":
		return service.Scanned{}, service.VaultMissing{Reason: domain.ReasonNotFound}
	case "none":
		return service.Scanned{}, service.ErrNotLinked
	case "stranger":
		return service.Scanned{}, service.ErrNotFound
	case "reader":
		return service.Scanned{}, service.ErrForbidden
	case "queued":
		// specs/2026-11-14-desk-obsidian-books.md OK0.4: a folder a book holds and one whose volume is in work of a queue.
		return service.Scanned{
			Folders: []service.ScannedFolder{
				{Folder: domain.Folder{Path: "Книга 1", Name: "Книга 1", Notes: 1, Skipped: domain.Skipped{}}, Depth: 1, Hint: &domain.Hint{Kind: domain.HintBook},
					Book: &service.BookFolder{Folder: "Книга 1", BookID: "b-1", BookTitle: "Застава"}},
				{Folder: domain.Folder{Path: "Книга 2", Name: "Книга 2", Notes: 1, Skipped: domain.Skipped{}}, Depth: 1, Hint: &domain.Hint{Kind: domain.HintBook},
					Book: &service.BookFolder{Folder: "Книга 2", Queued: true, QueueID: "q-1"}},
			},
			Skipped: domain.Skipped{},
		}, nil
	}
	return service.Scanned{
		Folders: []service.ScannedFolder{
			{Folder: domain.Folder{Path: domain.Root, Name: "Северный предел", Below: 1, Skipped: domain.Skipped{domain.SkipHidden: 1}}},
			{Folder: domain.Folder{Path: "Книга 1", Name: "Книга 1", Notes: 1, Skipped: domain.Skipped{}}, Depth: 1, Hint: &domain.Hint{Kind: domain.HintBook}},
		},
		Entries: 3, Skipped: domain.Skipped{domain.SkipHidden: 1, domain.SkipLink: 0},
	}, nil
}

type preview struct{}

func (preview) Execute(_ context.Context, input service.PreviewInput) (service.Preview, error) {
	switch input.ProjectID {
	case "large":
		return service.Preview{}, domain.TooLarge{Notes: 20001, Bytes: 1 << 20}
	case "crowded":
		return service.Preview{}, domain.TooLarge{Notes: 3, Bytes: 9, Entries: domain.MaxEntries + 1}
	case "overlap":
		return service.Preview{}, domain.Overlap{Outer: "Сага", Inner: "Сага/Книга 1"}
	case "path":
		return service.Preview{}, domain.ErrPath
	}
	return service.Preview{
		Books: []service.BookPreview{{
			Folder: input.Choice.Books[0], Title: "Застава", Chapters: []service.VolumeChapter{{Title: "Пролог", Characters: 40}},
			Characters: 40, Skipped: domain.Skipped{}, Fingerprint: "fp",
			Notes:    []domain.GluedNote{{Path: input.Choice.Books[0] + "/01 Пролог.md", Title: "Пролог", OrderBy: domain.OrderName, Chapters: []string{"Пролог"}}},
			Warnings: []service.BookWarning{{Code: domain.WarningOrderByName}},
		}},
		Materials: service.MaterialsPreview{
			Skipped: domain.Skipped{domain.SkipNoName: 1}, Folders: []service.FolderPrint{{Folder: "Персонажи", Fingerprint: "print"}},
			Records: []service.RecordPreview{
				{Record: domain.Record{Path: "Персонажи/Перевал.md"}, NoteState: domain.NoteState{State: domain.StateOtherSource, Target: "r-1", Seen: "seen-1"}, Folder: "Персонажи"},
				{Record: domain.Record{Path: "Персонажи/Ива.md"}, NoteState: domain.NoteState{State: domain.StateOtherSource, HeldBy: "Герои/Ива.md", Seen: "seen-2"}, Folder: "Персонажи"},
			},
		},
		NewChapters: 1, NewCharacters: 40, Reading: service.Reading{Helper: service.HelperNone},
	}, nil
}

func router() chi.Router {
	router := chi.NewRouter()
	Handlers{
		Read: bridge{}, Begin: begin{}, Unlink: unlink{}, Scan: scan{}, Preview: preview{}, Load: load{}, Update: update{},
		LinkBook: bookFolders{}, UnlinkBook: bookFolders{}, Materials: materials{}, Export: exports{}, Users: sessions{},
		AllowedOrigins: []string{origin},
	}.Mount(router)
	return router
}

func ask(method, path string, change func(*http.Request)) *httptest.ResponseRecorder {
	return askWith(method, path, "", change)
}

func askWith(method, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "author"})
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	router().ServeHTTP(response, request)
	return response
}

func code(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &problem)
	return problem.Code
}

// specs/2026-11-13-desk-obsidian.md OB1.1: a read needs the session of the window, a change its Origin and its
// request-forgery token too; the number of a choice goes to the page, a window without its shell is
// obsidian.choice_unavailable.
func TestTheOperationsOfTheBridgeAreTheWindowsOwn(t *testing.T) {
	noSession := func(request *http.Request) { request.Header.Del("Cookie") }
	for _, path := range []string{"/projects/linked/obsidian", "/projects/linked/obsidian/tree"} {
		if response := ask(http.MethodGet, path, noSession); response.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without the session = %d", path, response.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := map[string]string{http.MethodPost: "/projects/linked/obsidian/choice", http.MethodDelete: "/projects/linked/obsidian"}[method]
		if response := ask(method, path, func(request *http.Request) { request.Header.Set("Origin", "https://example.test") }); response.Code != http.StatusForbidden {
			t.Fatalf("%s %s from another Origin = %d", method, path, response.Code)
		}
		if response := ask(method, path, func(request *http.Request) { request.Header.Set("X-CSRF-Token", "other") }); response.Code != http.StatusForbidden || code(t, response) != "csrf.invalid" {
			t.Fatalf("%s %s without the token = %d %s", method, path, response.Code, response.Body)
		}
		if response := ask(method, path, noSession); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without the session = %d", method, path, response.Code)
		}
	}
	response := ask(http.MethodPost, "/projects/linked/obsidian/choice", nil)
	var choice struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &choice); response.Code != http.StatusAccepted || err != nil || len(choice.Request) != 43 {
		t.Fatalf("a choice = %d %s", response.Code, response.Body)
	}
	if response := ask(http.MethodPost, "/projects/headless/obsidian/choice", nil); response.Code != http.StatusServiceUnavailable || code(t, response) != domain.CodeChoiceUnavailable {
		t.Fatalf("a window without its shell = %d %s", response.Code, response.Body)
	}
	if response := ask(http.MethodDelete, "/projects/linked/obsidian", nil); response.Code != http.StatusNoContent {
		t.Fatalf("an unlink = %d %s", response.Code, response.Body)
	}
	if response := ask(http.MethodGet, "/projects/linked/obsidian/tree?path=/etc", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("a look with a query = %d", response.Code)
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.1, OB0.3, OB1.5, OB2.1: the link names the path of the vault — the one absolute path
// an answer gives —; the tree names its folders relative to the vault; the refusals carry their codes, a busy gate its
// Retry-After.
func TestTheAnswersOfTheBridge(t *testing.T) {
	response := ask(http.MethodGet, "/projects/linked/obsidian", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"path":"/home/author/Северный предел"`) ||
		!strings.Contains(response.Body.String(), `"availability":"available"`) {
		t.Fatalf("the link = %d %s", response.Code, response.Body)
	}
	if response := ask(http.MethodGet, "/projects/other/obsidian", nil); !strings.Contains(response.Body.String(), `"refused":"not_folder"`) ||
		strings.Contains(response.Body.String(), `"path"`) {
		t.Fatalf("no link = %s", response.Body)
	}
	tree := ask(http.MethodGet, "/projects/linked/obsidian/tree", nil)
	if tree.Code != http.StatusOK || strings.Contains(tree.Body.String(), `"/`) || strings.Contains(tree.Body.String(), "home") ||
		!strings.Contains(tree.Body.String(), `"skipped":[{"reason":"hidden","count":1}]`) || !strings.Contains(tree.Body.String(), `"hint":{"kind":"book"}`) {
		t.Fatalf("the tree = %d %s", tree.Code, tree.Body)
	}
	for project, want := range map[string]struct {
		status int
		code   string
	}{
		"busy": {http.StatusTooManyRequests, domain.CodeBusy}, "slow": {http.StatusServiceUnavailable, domain.CodeSlow},
		"gone": {http.StatusConflict, domain.CodeVaultMissing}, "none": {http.StatusConflict, domain.CodeNotLinked},
		"stranger": {http.StatusNotFound, "not_found"}, "reader": {http.StatusForbidden, "forbidden"},
	} {
		response := ask(http.MethodGet, "/projects/"+project+"/obsidian/tree", nil)
		if response.Code != want.status || code(t, response) != want.code {
			t.Fatalf("%s = %d %s", project, response.Code, response.Body)
		}
		if project == "busy" && response.Header().Get("Retry-After") != "2" {
			t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
		}
		if project == "gone" && !strings.Contains(response.Body.String(), `"vaultReason":"not_found"`) {
			t.Fatalf("a vault that does not open = %s", response.Body)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB0.3, OB4.1: the choice of a preview comes in the body of a write of the window; a body
// that is not one is request.validation; the refusals of a choice carry their numbers and their folders.
func TestThePreviewOfALoad(t *testing.T) {
	const path = "/projects/linked/obsidian/preview"
	for _, body := range []string{"", "{}", `{"books":[]}`, `{"books":["Книга 1"],"path":"/etc"}`, `{"books":["Книга 1"]}{}`} {
		if response := askWith(http.MethodPost, path, body, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%q = %d %s", body, response.Code, response.Body)
		}
	}
	if response := askWith(http.MethodPost, path, `{"books":["Книга 1"]}`, func(request *http.Request) { request.Header.Set("X-CSRF-Token", "x") }); response.Code != http.StatusForbidden {
		t.Fatalf("without the token = %d", response.Code)
	}
	response := askWith(http.MethodPost, path, `{"books":["Книга 1"],"materials":[{"path":"Персонажи","kind":"character"}],"exclude":["Книга 1/Черновик.md"]}`, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"chapters":[{"title":"Пролог","characters":40}]`) ||
		!strings.Contains(response.Body.String(), `"state":{"kind":"new","changed":false,"inTrash":false}`) ||
		!strings.Contains(response.Body.String(), `"reading":{"newChapters":1,"newCharacters":40,"helper":"none","consent":false}`) ||
		!strings.Contains(response.Body.String(), `"warnings":[{"code":"order_by_name","notes":[]}]`) {
		t.Fatalf("a preview = %d %s", response.Code, response.Body)
	}
	// specs/2026-11-15-desk-obsidian-notes.md ON1.1, ON1.3, ON1.4: what the author sees of each record, whether a decision may
	// replace the record that keeps its name — or which note of the load has the name —, and the fingerprint of each folder
	// of materials.
	for _, want := range []string{
		`"state":"other_source","changed":false,"aliases":[],"properties":0,"warnings":[],"cut":0,"left":0,"seen":"seen-1","replaceable":true}`,
		`"seen":"seen-2","replaceable":false,"heldBy":"Герои/Ива.md"}`, `"folders":[{"folder":"Персонажи","fingerprint":"print"}]`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("a preview = %s, want %s", response.Body, want)
		}
	}
	for project, want := range map[string]string{
		"large":   `"notes":20001,"bytes":1048576,"limitNotes":20000,"limitBytes":209715200}`,
		"crowded": `"notes":3,"bytes":9,"limitNotes":20000,"limitBytes":209715200,"entries":200001,"limitEntries":200000`,
		"overlap": `"folders":["Сага","Сага/Книга 1"]`, "path": `"code":"request.validation"`,
	} {
		response := askWith(http.MethodPost, "/projects/"+project+"/obsidian/preview", `{"books":["Книга 1"]}`, nil)
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s = %d %s", project, response.Code, response.Body)
		}
	}
}
