package sagadraft

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// openChapterEdit is «Править главу» (specs/2026-11-05-voice-full-access.md A8.7): the one draft of the chapter.
func (handlers Handlers) openChapterEdit(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	draft, err := handlers.useCases.OpenEdit.Execute(r.Context(), actor, strings.TrimSpace(chi.URLParam(r, "chapterId")))
	handlers.respondDraft(w, r, http.StatusOK, draft, err)
}

// replaceChapter is «Заменить главу в книге».
func (handlers Handlers) replaceChapter(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expectedRevision"`
		Title            string `json:"title"`
	}
	if err := ReadBody(w, r, &body); err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	draft, err := handlers.useCases.Replace.Execute(r.Context(), actor, DraftID(r), body.ExpectedRevision, body.Title)
	handlers.respondDraft(w, r, http.StatusOK, draft, err)
}

// revertChapter is «Вернуть прежний текст».
func (handlers Handlers) revertChapter(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := ReadBody(w, r, &body); err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	draft, err := handlers.useCases.Revert.Execute(r.Context(), actor, DraftID(r), body.ExpectedRevision)
	handlers.respondDraft(w, r, http.StatusOK, draft, err)
}

// reopenChapter is «Открыть главу заново».
func (handlers Handlers) reopenChapter(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := ReadBody(w, r, &body); err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	draft, err := handlers.useCases.Reopen.Execute(r.Context(), actor, DraftID(r), body.ExpectedRevision)
	handlers.respondDraft(w, r, http.StatusOK, draft, err)
}
