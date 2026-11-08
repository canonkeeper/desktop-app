// Package sagadraft holds the use cases of the draft of a chapter as text (specs/2026-11-07-desktop-app.md, DA10,
// DA16.4): the list and the draft, its edit, its versions, the edit of a chapter of the book and «Добавить в книгу». The
// writing agent over the same draft is services/writingagent.
package sagadraft

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagadraft"
)

// The use cases of the section «Письмо» (specs/2026-11-01-writer-assistant-index.md, UC7;
// specs/2026-11-02-writer-workshop.md): members read, an editor or above writes and adds the chapter of a draft to a
// book. A store checks the role in the transaction of the write, behind the session's
// write proof.

const (
	defaultPageSize = 30
	maxPageSize     = 100
	operationCreate = "chapter_draft.create.v1"
)

// Actor is who reads the drafts of which project.
type Actor struct {
	UserID    string
	ProjectID string
}

// Write is an actor with the write proof of his session.
type Write struct {
	Actor
	Proof auth.SessionWriteProof
}

// Request is what the idempotency scope of a mutation is made of.
type Request struct {
	Key    string
	Method string
	Path   string
}

// Replay is the idempotency record of one mutation: a replay of the same request answers the first,
// another request under the key is domain.ErrConflict.
type Replay struct {
	ScopeHash   string
	OperationID string
	KeyHash     string
	RequestHash string
	At          time.Time
}

// NewReplay is the idempotency record of one mutation of the actor under the key of his request.
func NewReplay(actor Write, request Request, operation string, body any, at time.Time) (Replay, error) {
	key := strings.TrimSpace(request.Key)
	if length := utf8.RuneCountInString(key); length < 8 || length > 128 || !actor.Proof.Valid() {
		return Replay{}, domain.ErrInvalid
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Replay{}, domain.ErrInvalid
	}
	return Replay{
		ScopeHash:   hashText(strings.Join([]string{actor.UserID, request.Method, request.Path}, "\x00")),
		OperationID: operation, KeyHash: hashText(key), RequestHash: hashText(string(encoded)), At: at.UTC(),
	}, nil
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// ListStore reads the drafts of a project for one of its members.
type ListStore interface {
	ListChapterDrafts(ctx context.Context, actor Actor, cursor *domain.Cursor, limit int) (domain.Page, error)
}

// List is listChapterDrafts: the drafts of the project, newest first.
type List struct{ Store ListStore }

// PageView is a page of drafts; NextCursor continues it, empty on the last one.
type PageView struct {
	Drafts     []domain.Summary
	Total      int64
	NextCursor string
}

func (useCase List) Execute(ctx context.Context, actor Actor, cursorValue string, limit int) (PageView, error) {
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > maxPageSize {
		return PageView{}, domain.ErrInvalid
	}
	var cursor *domain.Cursor
	if cursorValue != "" {
		decoded, err := decodeCursor(cursorValue)
		if err != nil {
			return PageView{}, err
		}
		cursor = &decoded
	}
	page, err := useCase.Store.ListChapterDrafts(ctx, actor, cursor, limit)
	if err != nil {
		return PageView{}, err
	}
	view := PageView{Drafts: page.Drafts, Total: page.Total}
	if page.HasMore && len(page.Drafts) > 0 {
		last := page.Drafts[len(page.Drafts)-1]
		if view.NextCursor, err = encodeCursor(domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}); err != nil {
			return PageView{}, err
		}
	}
	return view, nil
}

type cursorBody struct {
	At string `json:"at"`
	ID string `json:"id"`
}

func encodeCursor(cursor domain.Cursor) (string, error) {
	raw, err := json.Marshal(cursorBody{At: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(value string) (domain.Cursor, error) {
	if len(value) > 256 {
		return domain.Cursor{}, domain.ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return domain.Cursor{}, domain.ErrInvalid
	}
	var body cursorBody
	if json.Unmarshal(raw, &body) != nil || body.ID == "" {
		return domain.Cursor{}, domain.ErrInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, body.At)
	if err != nil {
		return domain.Cursor{}, domain.ErrInvalid
	}
	return domain.Cursor{CreatedAt: at, ID: body.ID}, nil
}

// GetStore reads one draft for a member of its project.
type GetStore interface {
	GetChapterDraft(ctx context.Context, actor Actor, draftID string) (domain.Draft, error)
}

// Get is getChapterDraft: the whole draft with its last Job, read again while it works.
type Get struct{ Store GetStore }

func (useCase Get) Execute(ctx context.Context, actor Actor, draftID string) (domain.Draft, error) {
	return useCase.Store.GetChapterDraft(ctx, actor, draftID)
}

// CreateInput is what a new draft starts with; everything can be changed later.
type CreateInput struct {
	Title string       `json:"title"`
	Place domain.Place `json:"place"`
	Idea  string       `json:"idea"`
	Kind  string       `json:"kind"`
	Text  string       `json:"text"`
}

// CreateCommand is one new draft with its idempotency record.
type CreateCommand struct {
	Write
	Replay
	Title string
	Place domain.Place
	Idea  string
	Kind  domain.Kind
	Text  string
}

// CreateStore keeps a draft under its idempotency key; domain.ErrNoDestination for a place not in the saga.
type CreateStore interface {
	CreateChapterDraft(ctx context.Context, command CreateCommand) (domain.Draft, error)
}

// Create is createChapterDraft.
type Create struct {
	Store CreateStore
	Now   func() time.Time
}

func (useCase Create) Execute(ctx context.Context, actor Write, input CreateInput, request Request) (domain.Draft, error) {
	title, ok := domain.CleanTitle(input.Title)
	kind, known := domain.ParseKind(input.Kind)
	if !ok || !known || !domain.ValidBody(input.Idea, domain.MaxIdeaRunes) || !domain.ValidBody(input.Text, domain.MaxChapterRunes) || !ValidPlace(input.Place) {
		return domain.Draft{}, domain.ErrInvalid
	}
	if err := domain.CheckMarks(input.Text); err != nil {
		return domain.Draft{}, err
	}
	replay, err := NewReplay(actor, request, operationCreate, input, useCase.Now())
	if err != nil {
		return domain.Draft{}, err
	}
	return useCase.Store.CreateChapterDraft(ctx, CreateCommand{
		Write: actor, Replay: replay, Title: title, Place: input.Place, Idea: input.Idea, Kind: kind, Text: input.Text,
	})
}

// ValidPlace says a place that names a chapter names its book too.
func ValidPlace(place domain.Place) bool {
	return place.BookID != "" || place.AfterChapterID == ""
}

// UpdateStore applies an edit at the expected revision: domain.ErrConflict when the draft is not at it,
// domain.ErrBusy while a Job works on it, domain.ErrAdded once it is in the book.
type UpdateStore interface {
	UpdateChapterDraft(ctx context.Context, actor Write, draftID string, expected int64, change domain.Change) (domain.Draft, error)
}

// Update is updateChapterDraft: the author's edit, so nothing he typed is lost silently (R7.2, R7.3).
type Update struct{ Store UpdateStore }

func (useCase Update) Execute(ctx context.Context, actor Write, draftID string, expected int64, change domain.Change) (domain.Draft, error) {
	if !actor.Proof.Valid() || expected < 1 || change.Empty() {
		return domain.Draft{}, domain.ErrInvalid
	}
	if change.Title != nil {
		title, ok := domain.CleanTitle(*change.Title)
		if !ok {
			return domain.Draft{}, domain.ErrInvalid
		}
		change.Title = &title
	}
	for _, text := range []struct {
		value *string
		limit int
	}{{change.Idea, domain.MaxIdeaRunes}, {change.Text, domain.MaxChapterRunes}} {
		if text.value != nil && !domain.ValidBody(*text.value, text.limit) {
			return domain.Draft{}, domain.ErrInvalid
		}
	}
	if change.Place != nil && !ValidPlace(*change.Place) {
		return domain.Draft{}, domain.ErrInvalid
	}
	if change.Text != nil {
		if err := domain.CheckMarks(*change.Text); err != nil {
			return domain.Draft{}, err
		}
	}
	if change.CaseChoice != nil && *change.CaseChoice != "" {
		if _, known := domain.ParseCase(string(*change.CaseChoice)); !known {
			return domain.Draft{}, domain.ErrInvalid
		}
	}
	// A version before the edit is kept only of an edit of the text (WA7.7).
	if change.KeepVersion && change.Text == nil {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.UpdateChapterDraft(ctx, actor, draftID, expected, change)
}

// DeleteStore removes a draft at the expected revision; domain.ErrBusy while a Job works on it.
type DeleteStore interface {
	GetChapterDraft(ctx context.Context, actor Actor, draftID string) (domain.Draft, error)
	DeleteChapterDraft(ctx context.Context, actor Write, draftID string, expected int64) error
}

// DeleteHelperSession deletes the session of the helper of a draft with its native root tree, cancelling a turn
// that runs; a session gone already is no error.
type DeleteHelperSession func(ctx context.Context, actorUserID, chatID string) error

// Delete is deleteChapterDraft. A chapter already in the book stays there. The session of the helper goes first
// (specs/2026-11-02-writer-workshop.md W7.5): a draft left after a failure opens a new session at the next message,
// a session left after the draft would stay unseen.
type Delete struct {
	Store         DeleteStore
	DeleteSession DeleteHelperSession
}

func (useCase Delete) Execute(ctx context.Context, actor Write, draftID string, expected int64) error {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.ErrInvalid
	}
	draft, err := useCase.Store.GetChapterDraft(ctx, actor.Actor, draftID)
	if err != nil {
		return err
	}
	switch {
	case draft.Job.Active():
		return domain.ErrBusy
	case draft.Revision != expected:
		return domain.ErrConflict
	}
	if draft.ChatID != "" {
		if err := useCase.DeleteSession(ctx, actor.UserID, draft.ChatID); err != nil {
			return err
		}
	}
	return useCase.Store.DeleteChapterDraft(ctx, actor, draftID, expected)
}

// AddStore adds the chapter of a draft to its book in one transaction: a repeat answers the draft as it
// stands and adds nothing (R7.5). domain.ErrNoDestination without a place, domain.ErrNoText without a
// title or a chapter, domain.ErrBusy while a Job works, a *quota.Exceeded over the limits of the text.
type AddStore interface {
	AddChapterDraftToBook(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error)
}

// AddToBook is addChapterDraftToBook.
type AddToBook struct{ Store AddStore }

func (useCase AddToBook) Execute(ctx context.Context, actor Write, draftID string, expected int64) (domain.Draft, error) {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.Draft{}, domain.ErrInvalid
	}
	return useCase.Store.AddChapterDraftToBook(ctx, actor, draftID, expected)
}
