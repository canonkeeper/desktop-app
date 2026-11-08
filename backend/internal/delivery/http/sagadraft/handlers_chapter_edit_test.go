package sagadraft

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	service "canonkeeper/backend/internal/services/sagadraft"

	"github.com/go-chi/chi/v5"
)

// editStub is the store of a chapter edit: it keeps what it was asked and answers a draft that edits a chapter.
type editStub struct {
	err   error
	asked []string
}

func (stub *editStub) answer(call string) (domain.Draft, error) {
	stub.asked = append(stub.asked, call)
	draft := testDraftValue()
	draft.Edits = &domain.Edit{ChapterID: "chapter-2", ChapterTitle: "Вторая", BookID: "book-1", Revision: 4, PreviousRevision: 3, BookRevision: 4, BookWords: 3200}
	return draft, stub.err
}

func (stub *editStub) OpenChapterEdit(_ context.Context, _ service.Write, chapterID string) (domain.Draft, error) {
	return stub.answer("open " + chapterID)
}

func (stub *editStub) ReplaceChapterFromDraft(_ context.Context, _ service.Write, DraftID string, expected int64, title string) (domain.Draft, error) {
	return stub.answer("replace " + DraftID + " " + title)
}

func (stub *editStub) RevertChapterFromDraft(_ context.Context, _ service.Write, DraftID string, _ int64) (domain.Draft, error) {
	return stub.answer("revert " + DraftID)
}

func (stub *editStub) ReopenChapterFromBook(_ context.Context, _ service.Write, DraftID string, _ int64) (domain.Draft, error) {
	return stub.answer("reopen " + DraftID)
}

func editRouter(stub *editStub) http.Handler {
	mux := chi.NewRouter()
	NewHandlers(UseCases{
		OpenEdit: service.OpenChapterEdit{Store: stub}, Replace: service.ReplaceChapter{Store: stub}, Revert: service.RevertChapter{Store: stub}, Reopen: service.ReopenChapter{Store: stub},
	}, userStub{}, []string{"http://app.test"}).Mount(mux)
	return mux
}

// specs/2026-11-05-voice-full-access.md A8.7: the three operations of a chapter edit answer the draft with its edit.
func TestChapterEditAnswersTheDraftWithItsEdit(t *testing.T) {
	stub := &editStub{}
	opened := call(t, editRouter(stub), http.MethodPost, "/projects/"+testProject+"/chapters/chapter-2/edit", "", nil)
	if opened.Code != http.StatusOK {
		t.Fatalf("open = %d %s", opened.Code, opened.Body)
	}
	var body struct {
		Edits struct {
			ChapterID    string `json:"chapterId"`
			ChapterTitle string `json:"chapterTitle"`
			BookWords    int    `json:"bookWords"`
			Changed      bool   `json:"changed"`
			Gone         bool   `json:"gone"`
			CanRevert    bool   `json:"canRevert"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(opened.Body.Bytes(), &body); err != nil || body.Edits.ChapterID != "chapter-2" || body.Edits.BookWords != 3200 || !body.Edits.CanRevert || body.Edits.Changed || body.Edits.Gone {
		t.Fatalf("edit = %+v, %v", body.Edits, err)
	}
	if replaced := call(t, editRouter(stub), http.MethodPost, base+"/"+testDraft+"/replace-chapter", `{"expectedRevision":4,"title":" Глава 2 "}`, nil); replaced.Code != http.StatusOK {
		t.Fatalf("replace = %d %s", replaced.Code, replaced.Body)
	}
	if reopened := call(t, editRouter(stub), http.MethodPost, base+"/"+testDraft+"/reopen-chapter", `{"expectedRevision":5}`, nil); reopened.Code != http.StatusOK {
		t.Fatalf("reopen = %d %s", reopened.Code, reopened.Body)
	}
	if reverted := call(t, editRouter(stub), http.MethodPost, base+"/"+testDraft+"/revert-chapter", `{"expectedRevision":5}`, nil); reverted.Code != http.StatusOK {
		t.Fatalf("revert = %d %s", reverted.Code, reverted.Body)
	}
	want := []string{"open chapter-2", "replace " + testDraft + " Глава 2", "reopen " + testDraft, "revert " + testDraft}
	if !slices.Equal(stub.asked, want) {
		t.Fatalf("asked = %q", stub.asked)
	}
}

func TestChapterEditRefusalsHaveTheirCodes(t *testing.T) {
	for _, test := range []struct {
		err    error
		body   string
		status int
		code   string
	}{
		{domain.ErrChapterChanged, `{"expectedRevision":4,"title":"Глава"}`, http.StatusConflict, "chapter_draft.chapter_changed"},
		{domain.ErrChapterGone, `{"expectedRevision":4,"title":"Глава"}`, http.StatusConflict, "chapter_draft.chapter_gone"},
		{domain.ErrBusy, `{"expectedRevision":4,"title":"Глава"}`, http.StatusConflict, "chapter_draft.busy"},
		{nil, `{"expectedRevision":4,"title":"  "}`, http.StatusBadRequest, "request.validation"},
		{nil, `{"expectedRevision":0,"title":"Глава"}`, http.StatusBadRequest, "request.validation"},
	} {
		recorder := call(t, editRouter(&editStub{err: test.err}), http.MethodPost, base+"/"+testDraft+"/replace-chapter", test.body, nil)
		if recorder.Code != test.status || problemCode(t, recorder) != test.code {
			t.Fatalf("%v %s: %d %s", test.err, test.body, recorder.Code, recorder.Body)
		}
	}
	noProof := call(t, editRouter(&editStub{}), http.MethodPost, "/projects/"+testProject+"/chapters/chapter-2/edit", "", map[string]string{"Origin": "http://evil.test"})
	if noProof.Code != http.StatusForbidden {
		t.Fatalf("another site = %d", noProof.Code)
	}
}
