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
	// chat is the chat the list was read for (specs/2026-11-11-ux5-proofread-mode.md PM3.3).
	chat string
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

func (stub *storeStub) ListChapterDrafts(_ context.Context, _ service.Actor, _ *domain.Cursor, _ int, chat string) (domain.Page, error) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	stub.chat = chat
	summary := domain.Summary{ID: testDraft, Title: "Глава", Kind: domain.KindSketch, Stage: domain.StageText, Revision: 2, CreatedAt: at, UpdatedAt: at,
		Idea: true, Job: &domain.Job{ID: "job", Mode: jobs.ChapterDraftScenes, Status: "running", Phase: "scenes", Done: 1, Total: 3},
		Plan: &domain.PlanState{Number: 2, Approved: true, Reason: "first"}, Findings: 4, Corrections: 1}
	if chat != "" {
		summary.Attachment = &domain.Attachment{ChatID: chat, Number: 2}
	}
	return domain.Page{Drafts: []domain.Summary{summary}, Total: 1}, stub.err
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
	if _, holdsText := page.Data[0]["text"]; holdsText || page.Data[0]["stage"] != "text" {
		t.Fatalf("a list row = %v", page.Data[0])
	}
	// specs/2026-11-11-ux5-writing-path.md WP1.2: a row names its step by what the server knows of it — the idea, the plan,
	// the Job and its course, the open findings and corrections — in the fields the draft has too.
	row, _ := json.Marshal(page.Data[0])
	for _, want := range []string{`"idea":true`, `"job":{"done":1,"id":"job","mode":"scenes","phase":"scenes","status":"running","total":3}`,
		`"plan":{"approved":true,"number":2,"reason":"first","stale":false}`, `"findings":4`, `"corrections":1`} {
		if !strings.Contains(string(row), want) {
			t.Fatalf("a list row = %s, want %s", row, want)
		}
	}
	if _, old := page.Data[0]["jobStatus"]; old {
		t.Fatalf("a list row keeps the status apart from its Job: %s", row)
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

// newChaptersStub answers what the tiers say of a draft, and counts the drafts it was asked of.
type newChaptersStub struct {
	answer bool
	err    error
	asked  []string
}

func (stub *newChaptersStub) NewChapter(_ context.Context, draft domain.Draft) (bool, error) {
	stub.asked = append(stub.asked, draft.ID)
	return stub.answer, stub.err
}

// newChapterOf is the newChapter of an answered draft: nil when the answer carries none.
func newChapterOf(t *testing.T, recorder *httptest.ResponseRecorder) *bool {
	t.Helper()
	var draft struct {
		NewChapter *bool `json:"newChapter"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &draft); err != nil {
		t.Fatalf("%s: %v", recorder.Body.String(), err)
	}
	return draft.NewChapter
}

// specs/2026-11-10-ux1-writing.md UW1.1, UW1.2: every answer with a draft says whether its first scenes would count as a
// new chapter of the writer's tier, by the port of the build; a build without tiers and a chapter in the book say
// nothing, and a read that failed leaves the sign out without failing the answer.
func TestADraftSaysWhetherItsScenesCountAsANewChapter(t *testing.T) {
	draft := testDraftValue()
	for _, answer := range []bool{true, false} {
		chapters := &newChaptersStub{answer: answer}
		view := DraftAnswer(context.Background(), chapters, draft)
		if view.NewChapter == nil || *view.NewChapter != answer || len(chapters.asked) != 1 || chapters.asked[0] != testDraft {
			t.Fatalf("a draft of a build with tiers = %v, asked %v; want %t", view.NewChapter, chapters.asked, answer)
		}
	}
	if view := DraftAnswer(context.Background(), nil, draft); view.NewChapter != nil {
		t.Fatalf("a build without tiers says %v", *view.NewChapter)
	}
	added := draft
	added.AddedChapterID = "chapter-in-book"
	chapters := &newChaptersStub{answer: true}
	if view := DraftAnswer(context.Background(), chapters, added); view.NewChapter != nil || len(chapters.asked) != 0 {
		t.Fatalf("a chapter in the book says %v, asked %v", view.NewChapter, chapters.asked)
	}
	if view := DraftAnswer(context.Background(), &newChaptersStub{answer: true, err: context.DeadlineExceeded}, draft); view.NewChapter != nil || view.ID != testDraft {
		t.Fatalf("a failed read = %+v", view)
	}

	// The handlers answer through it: a read and a mutation alike.
	stub := &storeStub{}
	mux := chi.NewRouter()
	NewHandlers(UseCases{Get: service.Get{Store: stub}, Update: service.Update{Store: stub}}, userStub{}, []string{"http://app.test"}).
		WithNewChapters(&newChaptersStub{answer: true}).Mount(mux)
	if got := newChapterOf(t, call(t, mux, http.MethodGet, base+"/"+testDraft, "", nil)); got == nil || !*got {
		t.Fatalf("a read with tiers = %v", got)
	}
	if got := newChapterOf(t, call(t, mux, http.MethodPatch, base+"/"+testDraft, `{"expectedRevision":3,"title":"Глава"}`, nil)); got == nil || !*got {
		t.Fatalf("an edit with tiers = %v", got)
	}
	if got := newChapterOf(t, call(t, router(stub), http.MethodGet, base+"/"+testDraft, "", nil)); got != nil {
		t.Fatalf("a read without tiers = %v", *got)
	}
}

// specs/2026-11-11-ux5-proofread-mode.md PM1.4, PM3.3: the list of a chat reads its drafts by the chat and names the
// attachment each began from; the original of a draft names the revisions of its file.
func TestTheDraftsOfAChatAndTheRevisionsOfTheFileAnswerTheContract(t *testing.T) {
	stub := &storeStub{}
	handler := router(stub)
	chat := "90000000-0000-4000-8000-0000000000c1"
	list := call(t, handler, http.MethodGet, base+"?chat="+chat, "", nil)
	if list.Code != http.StatusOK || stub.chat != chat || !strings.Contains(list.Body.String(), `"attachment":{"chatId":"`+chat+`","number":2}`) {
		t.Fatalf("the drafts of a chat = %d %s (chat %q)", list.Code, list.Body, stub.chat)
	}
	if plain := call(t, handler, http.MethodGet, base, "", nil); stub.chat != "" || strings.Contains(plain.Body.String(), `"attachment"`) {
		t.Fatalf("the list of the saga = %s (chat %q)", plain.Body, stub.chat)
	}
	view := draftDTO(domain.Draft{ID: testDraft, Original: &domain.Original{FileName: "7.docx", Format: "docx", Words: 9, Revisions: 3}})
	body, err := json.Marshal(view)
	if err != nil || !strings.Contains(string(body), `"revisions":3`) {
		t.Fatalf("the original of a draft = %s, %v", body, err)
	}
	none, err := json.Marshal(draftDTO(domain.Draft{ID: testDraft, Original: &domain.Original{FileName: "7.docx", Format: "docx"}}))
	if err != nil || !strings.Contains(string(none), `"revisions":0`) {
		t.Fatalf("a file without revisions = %s, %v", none, err)
	}
}

// specs/2026-11-11-ux9-writing-b.md WB2.1: a finding of style answers its category in the author's words, by the one
// table of the check; another note has no label.
func TestAFindingOfStyleIsNamedInTheAuthorsWords(t *testing.T) {
	style := NoteDTO(domain.Note{Kind: domain.NoteStyle, Category: "стоп-лист", Body: "«являться» — заезженное слово: скажите проще"})
	if style.Label != "Штамп" || style.Category != "стоп-лист" {
		t.Fatalf("style note = %+v", style)
	}
	if canon := NoteDTO(domain.Note{Kind: domain.NoteCanon, Category: "стоп-лист"}); canon.Label != "" {
		t.Fatalf("a note of the canon has no style label: %+v", canon)
	}
}

// specs/2026-11-12-ux10-scene-stream.md ST1.4: the draft answers the scene in writing with its Job — its number, call, step,
// why it is written again and its text so far; without one the Job says nothing of it.
func TestTheSceneInWritingAnswersWithItsJob(t *testing.T) {
	job := &domain.Job{ID: "job", Mode: "scenes", Status: "running", Phase: "scenes", Done: 1, Total: 3,
		Writing: &domain.Writing{Scene: 2, Attempt: 2, Step: "again", Reason: "lost"}}
	body, err := json.Marshal(draftDTO(domain.Draft{ID: testDraft, Job: job}))
	if err != nil || !strings.Contains(string(body), `"writing":{"scene":2,"attempt":2,"step":"again","reason":"lost","text":""}`) {
		t.Fatalf("a scene written again = %s, %v", body, err)
	}
	job.Writing = &domain.Writing{Scene: 2, Attempt: 1, Step: "writing", Text: "Кира шла"}
	if body, _ = json.Marshal(draftDTO(domain.Draft{ID: testDraft, Job: job})); !strings.Contains(string(body), `"writing":{"scene":2,"attempt":1,"step":"writing","text":"Кира шла"}`) {
		t.Fatalf("a scene in writing = %s", body)
	}
	job.Writing = nil
	if body, _ = json.Marshal(draftDTO(domain.Draft{ID: testDraft, Job: job})); strings.Contains(string(body), `"writing"`) {
		t.Fatalf("a Job without a scene in writing = %s", body)
	}
}
