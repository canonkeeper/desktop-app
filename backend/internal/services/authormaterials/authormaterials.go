// Package authormaterials holds the use cases of the materials of the author (specs/2026-11-04-saga-map.md, UC9):
// project readers list the records, an editor or above changes, removes and imports them.
package authormaterials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagamemory"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	// maxParsedBytes bounds what one file keeps in its import.
	maxParsedBytes = 16 << 20
	// maxCalls bounds the model calls of one file.
	maxCalls = 60

	operationStart   = "author_materials.import.v1"
	operationConfirm = "author_materials.confirm.v1"
)

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

// Replay is what the idempotency record of a mutation is made of.
type Replay struct {
	ScopeHash   string
	OperationID string
	KeyHash     string
	RequestHash string
}

// RequestPath is what the idempotency scope of a mutation is made of.
type RequestPath struct {
	Key    string
	Method string
	Path   string
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func replayOf(actor Write, request RequestPath, operation, requestHash string) (Replay, error) {
	key := strings.TrimSpace(request.Key)
	if length := utf8.RuneCountInString(key); length < 8 || length > 128 || !actor.Proof.Valid() {
		return Replay{}, domain.ErrInvalid
	}
	return Replay{
		ScopeHash:   hashText(strings.Join([]string{actor.UserID, request.Method, request.Path}, "\x00")),
		OperationID: operation, KeyHash: hashText(key), RequestHash: requestHash,
	}, nil
}

// ListStore reads the records of a project for one of its members.
type ListStore interface {
	ListAuthorMaterials(ctx context.Context, actor Actor, filter domain.Filter) (domain.Page, error)
}

// List is listAuthorMaterials: the records of the project by name.
type List struct{ Store ListStore }

// ListInput selects a page; empty Kind and Query select everything.
type ListInput struct {
	Kind   string
	Query  string
	Cursor string
	Limit  int
}

func (useCase List) Execute(ctx context.Context, actor Actor, input ListInput) (domain.Page, error) {
	filter := domain.Filter{Limit: input.Limit, AfterKey: input.Cursor}
	if filter.Limit == 0 {
		filter.Limit = defaultPageSize
	}
	if filter.Limit < 1 || filter.Limit > maxPageSize || utf8.RuneCountInString(input.Cursor) > 400 {
		return domain.Page{}, domain.ErrInvalid
	}
	if input.Kind != "" {
		kind, ok := domain.ParseKind(input.Kind)
		if !ok {
			return domain.Page{}, domain.ErrInvalid
		}
		filter.Kinds = []domain.Kind{kind}
	}
	query, ok := domain.CleanQuery(input.Query)
	if !ok {
		return domain.Page{}, domain.ErrInvalid
	}
	filter.Query = query
	return useCase.Store.ListAuthorMaterials(ctx, actor, filter)
}

// UpdateStore replaces what a record says when it is still at the expected revision; domain.ErrConflict when it is not
// or another record has the name, domain.ErrNotFound without one.
type UpdateStore interface {
	UpdateAuthorMaterial(ctx context.Context, actor Write, recordID string, expected int64, draft domain.Draft, key string) (domain.Record, error)
}

// Update is updateAuthorMaterial: the author changes what a record says.
type Update struct{ Store UpdateStore }

func (useCase Update) Execute(ctx context.Context, actor Write, recordID string, expected int64, draft domain.Draft) (domain.Record, error) {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.Record{}, domain.ErrInvalid
	}
	clean, ok := domain.CleanDraft(draft)
	if !ok || sagamemory.GlossaryKey(clean.Name) == "" {
		return domain.Record{}, domain.ErrInvalid
	}
	return useCase.Store.UpdateAuthorMaterial(ctx, actor, recordID, expected, clean, sagamemory.GlossaryKey(clean.Name))
}

// DeleteStore removes a record at the expected revision.
type DeleteStore interface {
	DeleteAuthorMaterial(ctx context.Context, actor Write, recordID string, expected int64) error
}

// Delete is deleteAuthorMaterial.
type Delete struct{ Store DeleteStore }

func (useCase Delete) Execute(ctx context.Context, actor Write, recordID string, expected int64) error {
	if !actor.Proof.Valid() || expected < 1 {
		return domain.ErrInvalid
	}
	return useCase.Store.DeleteAuthorMaterial(ctx, actor, recordID, expected)
}

// StartCommand is one import with what its file holds.
type StartCommand struct {
	Write
	Replay
	SourceName string
	SourceKind domain.SourceKind
	Parsed     []byte
	At         time.Time
}

// StartStore queues the Job of an import in one transaction with the quota of the day, the import and the check that
// no import of the project is running (domain.ErrImportBusy). A replay of the same request returns the import it made.
type StartStore interface {
	StartAuthorMaterialsImport(ctx context.Context, command StartCommand) (domain.Import, error)
}

// Start is startAuthorMaterialsImport: the file is read here and only what it holds is kept (R9.2, R9.3).
type Start struct {
	Store StartStore
	Now   func() time.Time
}

// StartInput is a file or a pasted text.
type StartInput struct {
	FileName string
	Content  []byte
	Text     string
}

func (useCase Start) Execute(ctx context.Context, actor Write, input StartInput, request RequestPath) (domain.Import, error) {
	parsed, kind, err := readSource(input)
	if err != nil {
		return domain.Import{}, err
	}
	calls := 0
	for _, section := range parsed.Sections {
		calls += len(section.Chunks())
	}
	if calls > maxCalls {
		return domain.Import{}, domain.ErrTooLarge
	}
	body, err := json.Marshal(parsed)
	if err != nil {
		return domain.Import{}, domain.ErrInvalid
	}
	if len(body) > maxParsedBytes {
		return domain.Import{}, domain.ErrTooLarge
	}
	replay, err := replayOf(actor, request, operationStart, hashText(input.FileName+"\x00"+string(body)))
	if err != nil {
		return domain.Import{}, err
	}
	return useCase.Store.StartAuthorMaterialsImport(ctx, StartCommand{
		Write: actor, Replay: replay, SourceName: domain.CleanSourceName(input.FileName), SourceKind: kind, Parsed: body, At: useCase.Now().UTC(),
	})
}

// readSource reads a file by what it holds, whatever its name (R9.2), or a pasted text.
func readSource(input StartInput) (domain.Parsed, domain.SourceKind, error) {
	if input.FileName != "" {
		if input.Text != "" {
			return domain.Parsed{}, "", domain.ErrInvalid
		}
		return domain.Parse(input.Content)
	}
	if input.Text == "" {
		return domain.Parsed{}, "", domain.ErrInvalid
	}
	parsed, err := domain.ParseText(input.Text)
	return parsed, domain.SourceText, err
}

// Match says what a candidate is against the saga (R9.4).
type Match string

const (
	MatchBook      Match = "book"
	MatchMaterials Match = "materials"
	MatchNew       Match = "new"
)

// CandidateView is a candidate with its match; BookName is the name the book knows it by, also when it matches a record.
type CandidateView struct {
	domain.Draft
	Match    Match
	BookName string
}

// ImportView is an import with its candidates matched, nil before its Job ends.
type ImportView struct {
	domain.Import
	Views []CandidateView
}

// ImportStore reads the import of a project and the name keys of its records for one of its members.
type ImportStore interface {
	AuthorMaterialsImport(ctx context.Context, actor Actor) (*domain.Import, map[string]bool, error)
}

// BookNames says what the book calls the given name keys, from the prepared saga map; a name it does not know is not in the answer.
type BookNames interface {
	BookNames(ctx context.Context, projectID string, keys []string) (map[string]string, error)
}

// GetImport is getAuthorMaterialsImport: the import the author has not answered, nil without one.
type GetImport struct {
	Store ImportStore
	Names BookNames
}

func (useCase GetImport) Execute(ctx context.Context, actor Actor) (*ImportView, error) {
	found, keys, err := useCase.Store.AuthorMaterialsImport(ctx, actor)
	if err != nil || found == nil {
		return nil, err
	}
	view := &ImportView{Import: *found}
	if found.Candidates == nil {
		return view, nil
	}
	names := make([]string, len(found.Candidates))
	for index, candidate := range found.Candidates {
		names[index] = sagamemory.GlossaryKey(candidate.Name)
	}
	book, err := useCase.Names.BookNames(ctx, actor.ProjectID, names)
	if err != nil {
		return nil, err
	}
	view.Views = make([]CandidateView, len(found.Candidates))
	for index, candidate := range found.Candidates {
		view.Views[index] = matched(candidate, book, keys)
	}
	return view, nil
}

func matched(candidate domain.Draft, book map[string]string, keys map[string]bool) CandidateView {
	view := CandidateView{Draft: candidate, Match: MatchNew}
	key := sagamemory.GlossaryKey(candidate.Name)
	if name, ok := book[key]; ok {
		view.Match, view.BookName = MatchBook, name
	}
	// Saving replaces a record of the author: that is what he must know first (R9.4).
	if keys[key] {
		view.Match = MatchMaterials
	}
	return view
}

// ConfirmCommand is the author's answer to an import.
type ConfirmCommand struct {
	Write
	Replay
	ImportID string
	Choices  []domain.Choice
}

// ConfirmStore keeps the chosen candidates in one transaction and removes the import: a new name is a new record, a
// name the project has updates its record. A replay of the same request answers the same count.
type ConfirmStore interface {
	ConfirmAuthorMaterialsImport(ctx context.Context, command ConfirmCommand) (int, error)
}

// Confirm is confirmAuthorMaterialsImport.
type Confirm struct{ Store ConfirmStore }

func (useCase Confirm) Execute(ctx context.Context, actor Write, importID string, choices []domain.Choice, request RequestPath) (int, error) {
	encoded, err := json.Marshal(choices)
	if err != nil {
		return 0, domain.ErrInvalid
	}
	replay, err := replayOf(actor, request, operationConfirm, hashText(importID+"\x00"+string(encoded)))
	if err != nil {
		return 0, err
	}
	return useCase.Store.ConfirmAuthorMaterialsImport(ctx, ConfirmCommand{Write: actor, Replay: replay, ImportID: importID, Choices: choices})
}

// DiscardStore removes an import whose Job has ended; domain.ErrImportBusy while it runs.
type DiscardStore interface {
	DiscardAuthorMaterialsImport(ctx context.Context, actor Write, importID string) error
}

// Discard is discardAuthorMaterialsImport.
type Discard struct{ Store DiscardStore }

func (useCase Discard) Execute(ctx context.Context, actor Write, importID string) error {
	if !actor.Proof.Valid() {
		return domain.ErrInvalid
	}
	return useCase.Store.DiscardAuthorMaterialsImport(ctx, actor, importID)
}
