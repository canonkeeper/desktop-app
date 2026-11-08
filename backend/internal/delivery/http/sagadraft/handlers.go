// Package sagadraft delivers the draft of a chapter as text over HTTP (specs/2026-11-07-desktop-app.md, DA10, DA16.4;
// specs/2026-11-02-writer-workshop.md): members read the drafts of a project; an editor or above creates, edits and
// removes them, takes back a version, edits a chapter of the book and adds the chapter of a draft to a book, each
// behind the session's write proof. The operations of the writing agent are delivered by delivery/http/writingagent,
// which answers with the same draft.
package sagadraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/sagadraft"

	"github.com/go-chi/chi/v5"
)

// maxBody fits a draft at its limits in the longest characters of its text.
const maxBody = 4 << 20

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// UseCases are the operations of the draft as text.
type UseCases struct {
	List      service.List
	Get       service.Get
	Create    service.Create
	Update    service.Update
	Delete    service.Delete
	AddToBook service.AddToBook
	// OpenEdit, Replace and Revert edit a chapter that is already in the book (A8.7).
	OpenEdit service.OpenChapterEdit
	Replace  service.ReplaceChapter
	Revert   service.RevertChapter
	Reopen   service.ReopenChapter
	// The versions of the text: read, and taken back whole or by a piece (WA7.5).
	ListVersions service.ListVersions
	GetVersion   service.GetVersion
	TakeVersion  service.TakeVersion
}

type Handlers struct {
	useCases       UseCases
	currentUser    CurrentUser
	allowedOrigins []string
}

func NewHandlers(useCases UseCases, currentUser CurrentUser, allowedOrigins []string) Handlers {
	if currentUser == nil {
		panic("chapter drafts HTTP: dependencies are required")
	}
	return Handlers{useCases: useCases, currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins)}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.allowedOrigins...))
		router.Get("/projects/{projectId}/chapter-drafts", handlers.list)
		router.Post("/projects/{projectId}/chapter-drafts", handlers.create)
		router.Get("/projects/{projectId}/chapter-drafts/{draftId}", handlers.get)
		router.Get("/projects/{projectId}/chapter-drafts/{draftId}/versions", handlers.listVersions)
		router.Get("/projects/{projectId}/chapter-drafts/{draftId}/versions/{number}", handlers.getVersion)
		router.Post("/projects/{projectId}/chapter-drafts/{draftId}/versions/{number}/take", handlers.takeVersion)
		router.Patch("/projects/{projectId}/chapter-drafts/{draftId}", handlers.update)
		router.Delete("/projects/{projectId}/chapter-drafts/{draftId}", handlers.delete)
		router.Post("/projects/{projectId}/chapter-drafts/{draftId}/add-to-book", handlers.addToBook)
		router.Post("/projects/{projectId}/chapters/{chapterId}/edit", handlers.openChapterEdit)
		router.Post("/projects/{projectId}/chapter-drafts/{draftId}/replace-chapter", handlers.replaceChapter)
		router.Post("/projects/{projectId}/chapter-drafts/{draftId}/revert-chapter", handlers.revertChapter)
		router.Post("/projects/{projectId}/chapter-drafts/{draftId}/reopen-chapter", handlers.reopenChapter)
	})
}

func projectID(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "projectId")) }

// DraftID is the draft the path names.
func DraftID(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "draftId")) }

func (handlers Handlers) reader(w http.ResponseWriter, r *http.Request) (service.Actor, bool) {
	return Reader(handlers.currentUser, w, r)
}

func (handlers Handlers) writer(w http.ResponseWriter, r *http.Request) (service.Write, bool) {
	return Writer(handlers.currentUser, w, r)
}

// Reader is the member of the project of the request who reads; false — the refusal is written.
func Reader(users CurrentUser, w http.ResponseWriter, r *http.Request) (service.Actor, bool) {
	user, err := users.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		WriteError(w, r.Context(), err)
		return service.Actor{}, false
	}
	return service.Actor{UserID: user.ID, ProjectID: projectID(r)}, true
}

// Writer is the actor of a mutation with the write proof of his session; false — the refusal is written.
func Writer(users CurrentUser, w http.ResponseWriter, r *http.Request) (service.Write, bool) {
	user, proof, err := users.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token"))
	if err != nil {
		WriteError(w, r.Context(), err)
		return service.Write{}, false
	}
	return service.Write{Actor: service.Actor{UserID: user.ID, ProjectID: projectID(r)}, Proof: proof}, true
}

// ReadBody reads the JSON body of a request into body; an unknown field or a body past the limit is invalid.
func ReadBody(w http.ResponseWriter, r *http.Request, body any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil || !utf8.Valid(raw) {
		return domain.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil {
		return domain.ErrInvalid
	}
	return nil
}

// OneKey reads the one Idempotency-Key of a mutation.
func OneKey(r *http.Request) (service.Request, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		return service.Request{}, false
	}
	return service.Request{Key: keys[0], Method: r.Method, Path: r.URL.Path}, true
}

func (handlers Handlers) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	query := map[string]string{}
	for name, list := range r.URL.Query() {
		if !slices.Contains([]string{"cursor", "limit"}, name) || len(list) != 1 {
			WriteError(w, r.Context(), domain.ErrInvalid)
			return
		}
		query[name] = list[0]
	}
	limit := 0
	if value, present := query["limit"]; present {
		var err error
		if limit, err = strconv.Atoi(value); err != nil || limit < 1 {
			WriteError(w, r.Context(), domain.ErrInvalid)
			return
		}
	}
	view, err := handlers.useCases.List.Execute(r.Context(), actor, query["cursor"], limit)
	if err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	WriteJSON(w, http.StatusOK, pageDTO(view))
}

func (handlers Handlers) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	draft, err := handlers.useCases.Get.Execute(r.Context(), actor, DraftID(r))
	respondDraft(w, r, http.StatusOK, draft, err)
}

type placeBody struct {
	BookID         string `json:"bookId"`
	AfterChapterID string `json:"afterChapterId"`
}

func (handlers Handlers) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body struct {
		Title string     `json:"title"`
		Idea  string     `json:"idea"`
		Kind  string     `json:"kind"`
		Text  string     `json:"text"`
		Place *placeBody `json:"place"`
	}
	request, keyed := OneKey(r)
	if err := ReadBody(w, r, &body); err != nil || !keyed {
		WriteError(w, r.Context(), domain.ErrInvalid)
		return
	}
	input := service.CreateInput{Title: body.Title, Idea: body.Idea, Kind: body.Kind, Text: body.Text}
	if body.Place != nil {
		input.Place = domain.Place{BookID: body.Place.BookID, AfterChapterID: body.Place.AfterChapterID}
	}
	draft, err := handlers.useCases.Create.Execute(r.Context(), actor, input, request)
	respondDraft(w, r, http.StatusCreated, draft, err)
}

func (handlers Handlers) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64           `json:"expectedRevision"`
		Title            *string         `json:"title"`
		Place            json.RawMessage `json:"place"`
		Idea             *string         `json:"idea"`
		Kind             *string         `json:"kind"`
		Text             *string         `json:"text"`
		CaseChoice       json.RawMessage `json:"caseChoice"`
		KeepVersion      bool            `json:"keepVersion"`
	}
	if err := ReadBody(w, r, &body); err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	change := domain.Change{Title: body.Title, Idea: body.Idea, Text: body.Text, KeepVersion: body.KeepVersion}
	if len(body.CaseChoice) > 0 {
		// An explicit null gives the case back to the author's words (specs/2026-11-06-writing-agent.md, WA1).
		var chosen *string
		if json.Unmarshal(body.CaseChoice, &chosen) != nil || chosen != nil && *chosen == "" {
			WriteError(w, r.Context(), domain.ErrInvalid)
			return
		}
		choice := domain.Case("")
		if chosen != nil {
			choice = domain.Case(*chosen)
		}
		change.CaseChoice = &choice
	}
	if body.Kind != nil {
		kind, known := domain.ParseKind(*body.Kind)
		if !known {
			WriteError(w, r.Context(), domain.ErrInvalid)
			return
		}
		change.Kind = &kind
	}
	if len(body.Place) > 0 {
		// An explicit null takes the draft out of any book.
		var place *placeBody
		decoder := json.NewDecoder(bytes.NewReader(body.Place))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&place) != nil {
			WriteError(w, r.Context(), domain.ErrInvalid)
			return
		}
		change.Place = &domain.Place{}
		if place != nil {
			change.Place.BookID, change.Place.AfterChapterID = place.BookID, place.AfterChapterID
		}
	}
	draft, err := handlers.useCases.Update.Execute(r.Context(), actor, DraftID(r), body.ExpectedRevision, change)
	respondDraft(w, r, http.StatusOK, draft, err)
}

func (handlers Handlers) delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()["expectedRevision"]
	if len(values) != 1 || len(r.URL.Query()) != 1 {
		WriteError(w, r.Context(), domain.ErrInvalid)
		return
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		WriteError(w, r.Context(), domain.ErrInvalid)
		return
	}
	if err := handlers.useCases.Delete.Execute(r.Context(), actor, DraftID(r), revision); err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (handlers Handlers) addToBook(w http.ResponseWriter, r *http.Request) {
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
	draft, err := handlers.useCases.AddToBook.Execute(r.Context(), actor, DraftID(r), body.ExpectedRevision)
	respondDraft(w, r, http.StatusOK, draft, err)
}

func respondDraft(w http.ResponseWriter, r *http.Request, status int, draft domain.Draft, err error) {
	if err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	WriteJSON(w, status, DraftDTO(draft))
}

// The JSON shapes of contracts/openapi.json (ChapterDraft* schemas).

type placeView struct {
	BookID            string `json:"bookId,omitempty"`
	BookTitle         string `json:"bookTitle,omitempty"`
	AfterChapterID    string `json:"afterChapterId,omitempty"`
	AfterChapterTitle string `json:"afterChapterTitle,omitempty"`
	AfterChapterGone  bool   `json:"afterChapterGone,omitempty"`
}

type jobView struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// editView is the chapter of the book a draft edits (A8.7).
type editView struct {
	ChapterID    string `json:"chapterId"`
	ChapterTitle string `json:"chapterTitle,omitempty"`
	BookID       string `json:"bookId,omitempty"`
	BookWords    int    `json:"bookWords"`
	Changed      bool   `json:"changed"`
	Gone         bool   `json:"gone"`
	CanRevert    bool   `json:"canRevert"`
}

type addedView struct {
	ChapterID string `json:"chapterId"`
	Title     string `json:"title,omitempty"`
}

// DraftView is a draft as the contract answers it (ChapterDraft).
type DraftView struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Idea         string     `json:"idea"`
	Kind         string     `json:"kind"`
	Stage        string     `json:"stage"`
	Text         string     `json:"text"`
	Place        placeView  `json:"place"`
	TypicalWords *int       `json:"typicalWords,omitempty"`
	Notes        []noteView `json:"notes"`
	// ChatID is the session of the helper: its feed, stream, cancellation and correction are the chat's (W1.2).
	ChatID    string     `json:"chatId,omitempty"`
	Job       *jobView   `json:"job,omitempty"`
	Added     *addedView `json:"added,omitempty"`
	Edits     *editView  `json:"edits,omitempty"`
	Revision  int64      `json:"revision"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	// Case is the case of the chapter by the author's words or his choice; CaseChoice is his choice, if any; Plan is
	// the latest version of the plan (specs/2026-11-06-writing-agent.md, WA1, WA4).
	Case       string         `json:"case"`
	CaseChoice string         `json:"caseChoice,omitempty"`
	Plan       *planStateView `json:"plan,omitempty"`
	// Original is the chapter the author loaded as a file (specs/2026-11-07-writing-proofread.md, PR7.5).
	Original *originalView `json:"original,omitempty"`
}

type originalView struct {
	FileName   string    `json:"fileName"`
	Format     string    `json:"format"`
	Words      int       `json:"words"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// DraftDTO is the draft as the contract answers it.
func DraftDTO(draft domain.Draft) DraftView {
	view := DraftView{
		ID: draft.ID, Title: draft.Title, Idea: draft.Idea, Kind: string(draft.Kind), Stage: string(draft.Stage()), Text: draft.Text,
		Place: placeView{
			BookID: draft.Place.BookID, BookTitle: draft.BookTitle,
			AfterChapterID: draft.Place.AfterChapterID, AfterChapterTitle: draft.AfterChapterTitle, AfterChapterGone: draft.PlaceGone,
		},
		TypicalWords: draft.TypicalWords, ChatID: draft.ChatID,
		Notes:    make([]noteView, len(draft.Notes)),
		Revision: draft.Revision, Author: draft.AuthorName, CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt,
		Case: string(draft.Case(draft.CaseChoice)), CaseChoice: string(draft.CaseChoice),
	}
	if draft.PlanNumber > 0 {
		view.Plan = &planStateView{Number: draft.PlanNumber, Approved: draft.PlanApproved, Stale: draft.PlanStale()}
	}
	for i, note := range draft.Notes {
		view.Notes[i] = NoteDTO(note)
	}
	if job := draft.Job; job != nil {
		view.Job = &jobView{
			ID: job.ID, Mode: job.Mode, Status: job.Status, Phase: job.Phase, Done: job.Done, Total: job.Total, ErrorCode: job.ErrorCode,
		}
	}
	if draft.AddedChapterID != "" {
		view.Added = &addedView{ChapterID: draft.AddedChapterID, Title: draft.AddedChapterTitle}
	}
	if original := draft.Original; original != nil {
		view.Original = &originalView{FileName: original.FileName, Format: original.Format, Words: original.Words, UploadedAt: original.LoadedAt}
	}
	if edit := draft.Edits; edit != nil {
		view.Edits = &editView{
			ChapterID: edit.ChapterID, ChapterTitle: edit.ChapterTitle, BookID: edit.BookID, BookWords: edit.BookWords,
			Changed: edit.Changed(), Gone: edit.Gone, CanRevert: edit.PreviousRevision > 0 && !edit.Gone && !edit.Changed(),
		}
	}
	return view
}

type summaryView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Stage     string    `json:"stage"`
	Words     int       `json:"words"`
	JobStatus string    `json:"jobStatus,omitempty"`
	Revision  int64     `json:"revision"`
	Edits     bool      `json:"edits,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type pageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int64   `json:"totalCount"`
}

type pageView struct {
	Data []summaryView `json:"data"`
	Meta pageMeta      `json:"meta"`
}

func pageDTO(view service.PageView) pageView {
	page := pageView{Data: make([]summaryView, len(view.Drafts)), Meta: pageMeta{TotalCount: view.Total}}
	for index, draft := range view.Drafts {
		page.Data[index] = summaryView{
			ID: draft.ID, Title: draft.Title, Kind: string(draft.Kind), Stage: string(draft.Stage), Words: draft.Words, JobStatus: draft.JobStatus,
			Revision: draft.Revision, Edits: draft.Edits, CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt,
		}
	}
	if view.NextCursor != "" {
		next := view.NextCursor
		page.Meta.HasMore, page.Meta.NextCursor = true, &next
	}
	return page
}

// WriteJSON answers a body that no cache keeps.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError answers the refusal of an operation of a draft by its problem code.
func WriteError(w http.ResponseWriter, ctx context.Context, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if httpapi.WriteQuotaProblem(w, err, time.Now()) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrInvalidCSRF):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
	case errors.Is(err, auth.ErrEmailNotVerified):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "email address is not verified", "auth.email_unverified")
	case errors.Is(err, domain.ErrForbidden):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "writing needs an editor", "forbidden")
	case errors.Is(err, domain.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "draft not found", "not_found")
	case errors.Is(err, domain.ErrBusy):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "a run is working on the draft", "chapter_draft.busy")
	case errors.Is(err, domain.ErrAdded):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the chapter is already in the book", "chapter_draft.added")
	case errors.Is(err, domain.ErrChapterChanged):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the chapter changed in the book", "chapter_draft.chapter_changed")
	case errors.Is(err, domain.ErrChapterGone):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the chapter is no longer in the book", "chapter_draft.chapter_gone")
	case errors.Is(err, domain.ErrConflict):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the draft changed", "conflict")
	case errors.Is(err, domain.ErrNoText):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the draft has nothing to work on", "chapter_draft.no_text")
	case errors.Is(err, domain.ErrNoDestination):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the saga has no such place", "chapter_draft.no_destination")
	case errors.Is(err, domain.ErrInvalidMarks):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "the marks of what the author did not write are broken", "chapter_draft.invalid_marks")
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, researchdomain.ErrValidation):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	// The turn of the helper goes the way of every chat turn (W1.2).
	case errors.Is(err, researchdomain.ErrOutdatedAgent):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the session belongs to an outdated helper", "chat.outdated_agent")
	case errors.Is(err, researchdomain.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "draft not found", "not_found")
	case errors.Is(err, researchdomain.ErrForbidden):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "writing needs an editor", "forbidden")
	case errors.Is(err, researchdomain.ErrConflict):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "request conflicts with the draft", "conflict")
	default:
		observability.ReportUnexpectedError(ctx, err)
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

type noteView struct {
	ID           string   `json:"id"`
	Number       int      `json:"number"`
	Kind         string   `json:"kind"`
	Target       string   `json:"target"`
	Origin       string   `json:"origin"`
	Section      string   `json:"section,omitempty"`
	Quote        string   `json:"quote,omitempty"`
	Body         string   `json:"body"`
	Replacement  string   `json:"replacement,omitempty"`
	Options      []string `json:"options,omitempty"`
	Refs         []string `json:"refs"`
	Answer       string   `json:"answer,omitempty"`
	Status       string   `json:"status"`
	VariantGroup string   `json:"variantGroup,omitempty"`
	// The card of a question of the writing agent (specs/2026-11-06-writing-agent.md, WA3.5).
	Blocks    string   `json:"blocks,omitempty"`
	Known     string   `json:"known,omitempty"`
	Effects   []string `json:"effects,omitempty"`
	Recommend string   `json:"recommend,omitempty"`
	Fallback  string   `json:"fallback,omitempty"`
	// A note of the writing agent (WA5.6, WA6): its level, the category of style, the book's quote of the canon, the scene.
	Level     string    `json:"level,omitempty"`
	Category  string    `json:"category,omitempty"`
	BookQuote string    `json:"bookQuote,omitempty"`
	Scene     int       `json:"scene,omitempty"`
	TurnID    string    `json:"turnId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NoteDTO is a note as the contract answers it.
func NoteDTO(note domain.Note) noteView {
	refs := note.Refs
	if refs == nil {
		refs = []string{}
	}
	return noteView{
		ID: note.ID, Number: note.Number, Kind: string(note.Kind), Target: string(note.Target), Origin: note.Origin, Section: note.Section,
		Quote: note.Quote, Body: note.Body, Replacement: note.Replacement, Options: note.Options, Refs: refs, Answer: note.Answer,
		Status: string(note.Status), VariantGroup: note.VariantGroup, TurnID: note.TurnID, CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt,
		Blocks: note.Blocks, Known: note.Known, Effects: note.Effects, Recommend: note.Recommend, Fallback: note.Fallback,
		Level: note.Level, Category: note.Category, BookQuote: note.BookQuote, Scene: note.Scene,
	}
}

// planStateView says on the draft which version of the plan is the latest and whether the author approved it.
type planStateView struct {
	Number   int  `json:"number"`
	Approved bool `json:"approved"`
	Stale    bool `json:"stale"`
}
