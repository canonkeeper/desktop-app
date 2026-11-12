// Package authormemory holds the use cases of the page «Память» (specs/2026-11-01-writer-assistant-index.md,
// R4.5): project readers list the records, an editor or above adds, changes and removes them.
package authormemory

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
	domain "canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
)

const (
	defaultPageSize = 30
	maxPageSize     = 100
	// operationCreate names the idempotency record of an added record.
	operationCreate = "author_memory.create.v1"
)

// textView is the text version the page names chapters in: the one of every web page of a saga.
var textView = string(governance.SourceViewPublished)

// Access says whether the actor may write in the project; domain.ErrNotFound for a project he is not
// in. Membership is the tenant predicate and goes first: nothing about the saga is read before it.
type Access interface {
	CanWriteAuthorMemory(ctx context.Context, actorUserID, projectID string) (bool, error)
}

// ChapterVersions reads the chapter order and keys of a text version, without the text.
type ChapterVersions interface {
	ChapterVersion(ctx context.Context, projectID, view string) (*sagamemory.Version, error)
}

// Actor is who reads the records of which project.
type Actor struct {
	UserID    string
	ProjectID string
}

// Write is an actor with the write proof of his session.
type Write struct {
	Actor
	Proof auth.SessionWriteProof
}

// Input is what the page sends for a record. Chapter is a chapter key, empty without one.
type Input struct {
	Kind    string
	Text    string
	Chapter string
	Name    string
}

// View is a record with the chapter it names, nil when it names none or the chapter is gone; ChapterGone says it named one
// the saga no longer has (specs/2026-11-10-ux8-memory-records.md MR1.2).
type View struct {
	domain.Record
	Chapter     *sagamemory.Chapter
	ChapterGone bool
}

// viewWith names the chapter of a record among the chapters of the saga.
func viewWith(record domain.Record, byID map[string]*sagamemory.Chapter) View {
	chapter := byID[record.ChapterID]
	return View{Record: record, Chapter: chapter, ChapterGone: record.ChapterID != "" && chapter == nil}
}

// PageView is a page of records; NextCursor continues it, empty on the last one. Shared says the saga has members besides
// one, so the page signs a record with its author (MR1.5).
type PageView struct {
	Records    []View
	Total      int64
	NextCursor string
	Shared     bool
}

func readInput(ctx context.Context, chapters ChapterVersions, projectID string, input Input) (domain.Draft, error) {
	kind, ok := domain.ParseKind(input.Kind)
	if !ok {
		return domain.Draft{}, domain.ErrInvalid
	}
	text, ok := domain.CleanText(input.Text)
	if !ok {
		return domain.Draft{}, domain.ErrInvalid
	}
	name, ok := domain.CleanName(input.Name)
	if !ok {
		return domain.Draft{}, domain.ErrInvalid
	}
	draft := domain.Draft{Kind: kind, Text: text, Name: name}
	key := strings.TrimSpace(input.Chapter)
	if key == "" {
		return draft, nil
	}
	if utf8.RuneCountInString(key) > 32 {
		return domain.Draft{}, domain.ErrNoChapter
	}
	version, err := chapters.ChapterVersion(ctx, projectID, textView)
	if err != nil {
		return domain.Draft{}, err
	}
	chapter, found := version.Find(key)
	if !found {
		return domain.Draft{}, domain.ErrNoChapter
	}
	draft.ChapterID = chapter.ID
	return draft, nil
}

func requireWriter(ctx context.Context, access Access, actor Actor) error {
	canWrite, err := access.CanWriteAuthorMemory(ctx, actor.UserID, actor.ProjectID)
	if err != nil {
		return err
	}
	if !canWrite {
		return domain.ErrForbidden
	}
	return nil
}

// ListStore reads the records of a project for one of its members.
type ListStore interface {
	ListAuthorMemory(ctx context.Context, actor Actor, filter domain.Filter) (domain.Page, error)
}

// List is listAuthorMemory: the records of the project, newest first.
type List struct {
	Store    ListStore
	Chapters ChapterVersions
}

// ListInput selects a page; empty Kind and Query select everything.
type ListInput struct {
	Kind   string
	Query  string
	JobID  string
	Cursor string
	Limit  int
}

func (useCase List) Execute(ctx context.Context, actor Actor, input ListInput) (PageView, error) {
	filter := domain.Filter{Limit: input.Limit}
	if filter.Limit == 0 {
		filter.Limit = defaultPageSize
	}
	if filter.Limit < 1 || filter.Limit > maxPageSize {
		return PageView{}, domain.ErrInvalid
	}
	if input.Kind != "" {
		kind, ok := domain.ParseKind(input.Kind)
		if !ok {
			return PageView{}, domain.ErrInvalid
		}
		filter.Kinds = []domain.Kind{kind}
	}
	query, ok := domain.CleanQuery(input.Query)
	if !ok {
		return PageView{}, domain.ErrInvalid
	}
	filter.Query, filter.JobID = query, input.JobID
	if input.Cursor != "" {
		cursor, err := decodeCursor(input.Cursor)
		if err != nil {
			return PageView{}, err
		}
		filter.Cursor = &cursor
	}
	page, err := useCase.Store.ListAuthorMemory(ctx, actor, filter)
	if err != nil {
		return PageView{}, err
	}
	result := PageView{Records: make([]View, len(page.Records)), Total: page.Total, Shared: page.Shared}
	byID, err := useCase.chaptersOf(ctx, actor.ProjectID, page.Records)
	if err != nil {
		return PageView{}, err
	}
	for index, record := range page.Records {
		result.Records[index] = viewWith(record, byID)
	}
	if page.HasMore && len(page.Records) > 0 {
		last := page.Records[len(page.Records)-1]
		if result.NextCursor, err = encodeCursor(domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}); err != nil {
			return PageView{}, err
		}
	}
	return result, nil
}

func (useCase List) chaptersOf(ctx context.Context, projectID string, records []domain.Record) (map[string]*sagamemory.Chapter, error) {
	wanted := false
	for _, record := range records {
		wanted = wanted || record.ChapterID != ""
	}
	if !wanted {
		return nil, nil
	}
	version, err := useCase.Chapters.ChapterVersion(ctx, projectID, textView)
	if err != nil {
		return nil, err
	}
	return chapterIndex(version), nil
}

func chapterIndex(version *sagamemory.Version) map[string]*sagamemory.Chapter {
	byID := make(map[string]*sagamemory.Chapter, len(version.Chapters))
	for index := range version.Chapters {
		byID[version.Chapters[index].ID] = &version.Chapters[index]
	}
	return byID
}

// viewOf names the chapter of one record.
func viewOf(ctx context.Context, chapters ChapterVersions, record domain.Record) (View, error) {
	if record.ChapterID == "" {
		return View{Record: record}, nil
	}
	version, err := chapters.ChapterVersion(ctx, record.ProjectID, textView)
	if err != nil {
		return View{}, err
	}
	return viewWith(record, chapterIndex(version)), nil
}

func encodeCursor(cursor domain.Cursor) (string, error) {
	raw, err := json.Marshal(cursorBody{At: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

type cursorBody struct {
	At string `json:"at"`
	ID string `json:"id"`
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

// CreateCommand is one added record with its idempotency record.
type CreateCommand struct {
	Write
	Draft       domain.Draft
	ScopeHash   string
	OperationID string
	KeyHash     string
	RequestHash string
	At          time.Time
}

// CreateStore keeps a record under its idempotency key: a replay of the same request returns the record
// it made, another request under the key is domain.ErrConflict.
type CreateStore interface {
	CreateAuthorMemory(ctx context.Context, command CreateCommand) (domain.Record, error)
}

// Create is createAuthorMemory.
type Create struct {
	Access   Access
	Store    CreateStore
	Chapters ChapterVersions
	Now      func() time.Time
}

// RequestPath is what the idempotency scope of a mutation is made of.
type RequestPath struct {
	Key    string
	Method string
	Path   string
}

func (useCase Create) Execute(ctx context.Context, actor Write, input Input, request RequestPath) (View, error) {
	key := strings.TrimSpace(request.Key)
	if length := utf8.RuneCountInString(key); length < 8 || length > 128 || !actor.Proof.Valid() {
		return View{}, domain.ErrInvalid
	}
	if err := requireWriter(ctx, useCase.Access, actor.Actor); err != nil {
		return View{}, err
	}
	draft, err := readInput(ctx, useCase.Chapters, actor.ProjectID, input)
	if err != nil {
		return View{}, err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return View{}, domain.ErrInvalid
	}
	record, err := useCase.Store.CreateAuthorMemory(ctx, CreateCommand{
		Write: actor, Draft: draft, OperationID: operationCreate, At: useCase.Now().UTC(),
		ScopeHash:   hashText(strings.Join([]string{actor.UserID, request.Method, request.Path}, "\x00")),
		KeyHash:     hashText(key),
		RequestHash: hashText(string(encoded)),
	})
	if err != nil {
		return View{}, err
	}
	return viewOf(ctx, useCase.Chapters, record)
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// UpdateStore replaces what a record says when it is still at the expected revision; domain.ErrConflict
// when it is not, domain.ErrNotFound without one.
type UpdateStore interface {
	UpdateAuthorMemory(ctx context.Context, actor Write, recordID string, expected int64, draft domain.Draft) (domain.Record, error)
}

// Update is updateAuthorMemory: the author changes everything a record says.
type Update struct {
	Access   Access
	Store    UpdateStore
	Chapters ChapterVersions
}

func (useCase Update) Execute(ctx context.Context, actor Write, recordID string, expected int64, input Input) (View, error) {
	if !actor.Proof.Valid() || expected < 1 {
		return View{}, domain.ErrInvalid
	}
	if err := requireWriter(ctx, useCase.Access, actor.Actor); err != nil {
		return View{}, err
	}
	draft, err := readInput(ctx, useCase.Chapters, actor.ProjectID, input)
	if err != nil {
		return View{}, err
	}
	record, err := useCase.Store.UpdateAuthorMemory(ctx, actor, recordID, expected, draft)
	if err != nil {
		return View{}, err
	}
	return viewOf(ctx, useCase.Chapters, record)
}

// DeleteStore removes a record at the expected revision; domain.ErrConflict when it is not at it,
// domain.ErrNotFound without one.
type DeleteStore interface {
	DeleteAuthorMemory(ctx context.Context, actor Write, recordID string, expected int64) error
}

// Delete is deleteAuthorMemory.
type Delete struct {
	Access Access
	Store  DeleteStore
}

func (useCase Delete) Execute(ctx context.Context, actor Write, recordID string, expected int64) error {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.ErrInvalid
	}
	if err := requireWriter(ctx, useCase.Access, actor.Actor); err != nil {
		return err
	}
	return useCase.Store.DeleteAuthorMemory(ctx, actor, recordID, expected)
}
