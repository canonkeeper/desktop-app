package sagadraft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagadraft"

	"github.com/go-chi/chi/v5"
)

const (
	testProject = "90000000-0000-4000-8000-000000000001"
	testDraft   = "90000000-0000-4000-8000-000000000002"
)

type userStub struct{}

func (userStub) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	if cookie == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "user-1"}, nil
}

func (userStub) AuthenticateWrite(_ context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	if cookie == "" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrUnauthenticated
	}
	if csrf != "token" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return auth.User{ID: "user-1"}, auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}, nil
}

type storeStub struct {
	err     error
	created []service.CreateCommand
	change  domain.Change
	added   int64
	removed int64
	// idle is a draft no Job works on, with the session of its helper.
	idle     bool
	sessions []string
}

var typicalWords = 2800

func testDraftValue() domain.Draft {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	return domain.Draft{
		ID: testDraft, ProjectID: testProject, Title: "Глава", Idea: "встреча", Kind: domain.KindSketch, Text: "набросок", Revision: 3, TypicalWords: &typicalWords,
		Place: domain.Place{BookID: "book", AfterChapterID: "chapter"}, BookTitle: "Том", AfterChapterTitle: "Вторая",
		Job: &domain.Job{ID: "job", Mode: jobs.ChapterDraftScenes, Status: "running", Phase: "scenes", Done: 2, Total: 5},
		Notes: []domain.Note{{ID: "note", Number: 1, Kind: domain.NoteMeaning, Target: domain.TargetDraft, Origin: domain.OriginJob,
			Quote: "Он взял меч.", Body: "кто взял", Replacement: "Она взяла меч.", Status: domain.NoteApplied}},
		AuthorName: "Лена", CreatedAt: at, UpdatedAt: at,
	}
}

func (stub *storeStub) ListChapterDrafts(context.Context, service.Actor, *domain.Cursor, int) (domain.Page, error) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	return domain.Page{Drafts: []domain.Summary{{ID: testDraft, Title: "Глава", Kind: domain.KindSketch, Stage: domain.StageText, JobStatus: "running", Revision: 2, CreatedAt: at, UpdatedAt: at}}, Total: 1}, stub.err
}

func (stub *storeStub) GetChapterDraft(context.Context, service.Actor, string) (domain.Draft, error) {
	draft := testDraftValue()
	if stub.idle {
		draft.Job, draft.ChatID = nil, "chat-1"
	}
	return draft, stub.err
}

func (stub *storeStub) CreateChapterDraft(_ context.Context, command service.CreateCommand) (domain.Draft, error) {
	stub.created = append(stub.created, command)
	return testDraftValue(), stub.err
}

func (stub *storeStub) UpdateChapterDraft(_ context.Context, _ service.Write, _ string, _ int64, change domain.Change) (domain.Draft, error) {
	stub.change = change
	return testDraftValue(), stub.err
}

func (stub *storeStub) DeleteChapterDraft(_ context.Context, _ service.Write, _ string, expected int64) error {
	stub.removed = expected
	return stub.err
}

func (stub *storeStub) AddChapterDraftToBook(_ context.Context, _ service.Write, _ string, expected int64) (domain.Draft, error) {
	stub.added = expected
	return testDraftValue(), stub.err
}

func router(stub *storeStub) http.Handler {
	mux := chi.NewRouter()
	NewHandlers(UseCases{
		List: service.List{Store: stub}, Get: service.Get{Store: stub}, Create: service.Create{Store: stub, Now: time.Now},
		Update: service.Update{Store: stub}, Delete: service.Delete{Store: stub, DeleteSession: func(_ context.Context, _, chatID string) error {
			stub.sessions = append(stub.sessions, chatID)
			return nil
		}},
		AddToBook: service.AddToBook{Store: stub},
	}, userStub{}, []string{"http://app.test"}).Mount(mux)
	return mux
}

func call(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", "http://app.test")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
	request.Header.Set("X-CSRF-Token", "token")
	for name, value := range headers {
		if value == "" {
			request.Header.Del(name)
		} else {
			request.Header.Set(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func problemCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("%s: %v", recorder.Body.String(), err)
	}
	return problem.Code
}

const base = "/projects/" + testProject + "/chapter-drafts"

// R7.1: readers read: the whole draft with the progress of its Job, the list without its text.
func TestReadsAnswerTheContractShape(t *testing.T) {
	handler := router(&storeStub{})
	list := call(t, handler, http.MethodGet, base, "", nil)
	if list.Code != http.StatusOK || list.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	var page struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			TotalCount int `json:"totalCount"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Meta.TotalCount != 1 {
		t.Fatalf("page = %s, %v", list.Body, err)
	}
	if _, holdsText := page.Data[0]["text"]; holdsText || page.Data[0]["stage"] != "text" || page.Data[0]["jobStatus"] != "running" {
		t.Fatalf("a list row = %v", page.Data[0])
	}
	got := call(t, handler, http.MethodGet, base+"/"+testDraft, "", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d %s", got.Code, got.Body)
	}
	var draft struct {
		Idea         string `json:"idea"`
		TypicalWords int    `json:"typicalWords"`
		Stage        string `json:"stage"`
		Place        struct {
			BookTitle         string `json:"bookTitle"`
			AfterChapterTitle string `json:"afterChapterTitle"`
		} `json:"place"`
		Job struct {
			Phase string `json:"phase"`
			Done  int    `json:"done"`
			Total int    `json:"total"`
		} `json:"job"`
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &draft); err != nil || draft.Idea != "встреча" || draft.TypicalWords != 2800 || draft.Stage != "text" || draft.Place.AfterChapterTitle != "Вторая" ||
		draft.Job.Phase != "scenes" || draft.Job.Done != 2 || draft.Job.Total != 5 || draft.Revision != 3 {
		t.Fatalf("draft = %s, %v", got.Body, err)
	}
	if signedOut := call(t, handler, http.MethodGet, base, "", map[string]string{"Cookie": ""}); signedOut.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", signedOut.Code)
	}
}

// R7.1: a mutation needs its write proof, and those that start something one Idempotency-Key.
func TestMutationsAreBehindTheirProof(t *testing.T) {
	stub := &storeStub{}
	handler := router(stub)
	keyed := map[string]string{"Idempotency-Key": "key-12345"}
	if noCSRF := call(t, handler, http.MethodPost, base, `{"kind":"sketch"}`, map[string]string{"X-CSRF-Token": "", "Idempotency-Key": "key-12345"}); noCSRF.Code != http.StatusForbidden {
		t.Fatalf("no CSRF = %d", noCSRF.Code)
	}
	if noKey := call(t, handler, http.MethodPost, base, `{"kind":"sketch"}`, nil); noKey.Code != http.StatusBadRequest {
		t.Fatalf("no key = %d", noKey.Code)
	}
	if unknown := call(t, handler, http.MethodPost, base, `{"kind":"sketch","extra":1}`, keyed); unknown.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d", unknown.Code)
	}
	created := call(t, handler, http.MethodPost, base, `{"kind":"sketch","title":"Глава","idea":"встреча","text":"набросок","place":{"bookId":"book","afterChapterId":"chapter"}}`, keyed)
	if created.Code != http.StatusCreated || len(stub.created) != 1 || stub.created[0].Place.AfterChapterID != "chapter" || stub.created[0].Idea != "встреча" {
		t.Fatalf("created = %d %s", created.Code, created.Body)
	}
	// specs/2026-11-02-writer-workshop.md, W8.1: what should happen is not a kind, and a run has no mode.
	if skeleton := call(t, handler, http.MethodPost, base, `{"kind":"skeleton","text":"встреча"}`, map[string]string{"Idempotency-Key": "key-67890"}); skeleton.Code != http.StatusBadRequest {
		t.Fatalf("a draft of what should happen = %d", skeleton.Code)
	}
	if skeleton := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"kind":"skeleton"}`, nil); skeleton.Code != http.StatusBadRequest {
		t.Fatalf("an edit into what should happen = %d", skeleton.Code)
	}
	if added := call(t, handler, http.MethodPost, base+"/"+testDraft+"/add-to-book", `{"expectedRevision":3}`, nil); added.Code != http.StatusOK || stub.added != 3 {
		t.Fatalf("added = %d %s", added.Code, added.Body)
	}
	if busy := call(t, handler, http.MethodDelete, base+"/"+testDraft+"?expectedRevision=3", "", nil); busy.Code != http.StatusConflict || stub.removed != 0 {
		t.Fatalf("removed while a Job works = %d %s", busy.Code, busy.Body)
	}
	// specs/2026-11-02-writer-workshop.md W7.5: the session of the helper goes first, then the draft.
	stub.idle = true
	if removed := call(t, handler, http.MethodDelete, base+"/"+testDraft+"?expectedRevision=3", "", nil); removed.Code != http.StatusNoContent || stub.removed != 3 ||
		len(stub.sessions) != 1 || stub.sessions[0] != "chat-1" {
		t.Fatalf("removed = %d %s, sessions %v", removed.Code, removed.Body, stub.sessions)
	}
	if noRevision := call(t, handler, http.MethodDelete, base+"/"+testDraft, "", nil); noRevision.Code != http.StatusBadRequest {
		t.Fatalf("a removal without a revision = %d", noRevision.Code)
	}
}

// R7.2: a PATCH changes what it names: an absent place stays, an explicit null takes it away.
func TestPatchChangesWhatItNames(t *testing.T) {
	stub := &storeStub{}
	handler := router(stub)
	if patched := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"text":"глава","idea":"замысел"}`, nil); patched.Code != http.StatusOK {
		t.Fatalf("patched = %d %s", patched.Code, patched.Body)
	}
	if stub.change.Text == nil || *stub.change.Text != "глава" || *stub.change.Idea != "замысел" || stub.change.Place != nil {
		t.Fatalf("change = %+v", stub.change)
	}
	// specs/2026-11-06-writing-agent.md WA10.4: the draft has one working text.
	if chapter := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"chapter":"глава"}`, nil); chapter.Code != http.StatusBadRequest {
		t.Fatalf("an edit of the chapter text = %d", chapter.Code)
	}
	if plan := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"plan":"¶1"}`, nil); plan.Code != http.StatusBadRequest {
		t.Fatalf("an edit of a plan = %d", plan.Code)
	}
	if patched := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"place":null}`, nil); patched.Code != http.StatusOK || stub.change.Place == nil || stub.change.Place.BookID != "" {
		t.Fatalf("a null place = %d, %+v", patched.Code, stub.change)
	}
	if patched := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"place":{"bookId":"book"}}`, nil); patched.Code != http.StatusOK || stub.change.Place.BookID != "book" {
		t.Fatalf("a place = %d, %+v", patched.Code, stub.change)
	}
	if marks := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"text":"а ⟦б"}`, nil); marks.Code != http.StatusBadRequest || problemCode(t, marks) != "chapter_draft.invalid_marks" {
		t.Fatalf("broken marks = %d %s", marks.Code, marks.Body)
	}
	if bad := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"kind":"fact"}`, nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind = %d", bad.Code)
	}
	if empty := call(t, handler, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3}`, nil); empty.Code != http.StatusBadRequest {
		t.Fatalf("an empty edit = %d", empty.Code)
	}
}
