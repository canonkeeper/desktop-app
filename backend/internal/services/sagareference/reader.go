// Package sagareference serves the saga map (specs/2026-11-04-saga-map.md) — the graph of the saga and
// the card of a node — and the reads other pages share: the glossary and the contents of the saga
// (specs/2026-10-14-saga-reference.md). They read the same saga memory as the chat tools, after
// checking that the actor may read the text version.
package sagareference

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/sagamemory"
)

var (
	ErrInvalid  = errors.New("saga reference: invalid request")
	ErrNotFound = errors.New("saga reference: not found")
	ErrDenied   = errors.New("saga reference: access denied")
)

const (
	maxQueryRunes = 200
	maxKeyRunes   = 200
)

// Access checks that the actor may read a text version of the project: any member reads the
// published one, the draft needs an editor or above. It returns ErrNotFound or ErrDenied.
type Access interface {
	ReadableView(ctx context.Context, actorUserID, projectID, view string) error
}

// Memory is the saga memory the chat tools read too, per request
// (specs/2026-10-28-saga-search-in-postgres.md, UC3): the outline of the text version and the cards.
type Memory interface {
	Outline(ctx context.Context, projectID, view string) (*sagamemory.Outline, error)
	Cards(ctx context.Context, outline *sagamemory.Outline, from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error)
}

// Scope names the text version an actor reads.
type Scope struct {
	ActorUserID string
	ProjectID   string
	View        string
}

type reader struct {
	access Access
	memory Memory
}

func newReader(access Access, memory Memory) reader {
	if access == nil || memory == nil {
		panic("saga reference: dependencies are required")
	}
	return reader{access: access, memory: memory}
}

// load checks access and returns the outline and the parts of the cards of the version a page
// prints.
func (reader reader) load(ctx context.Context, scope Scope, parts sagamemory.CardParts) (*sagamemory.Outline, *sagamemory.CardSet, error) {
	outline, err := reader.outline(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	cards, err := reader.memory.Cards(ctx, outline, 0, -1, parts)
	if err != nil {
		return nil, nil, err
	}
	return outline, cards, nil
}

// outline checks access and returns the outline of the version.
func (reader reader) outline(ctx context.Context, scope Scope) (*sagamemory.Outline, error) {
	scope, err := readable(ctx, reader.access, scope)
	if err != nil {
		return nil, err
	}
	return reader.memory.Outline(ctx, scope.ProjectID, scope.View)
}

// readable checks the scope and that the actor may read its version.
func readable(ctx context.Context, access Access, scope Scope) (Scope, error) {
	scope.ActorUserID = strings.TrimSpace(scope.ActorUserID)
	scope.ProjectID = strings.TrimSpace(scope.ProjectID)
	if ctx == nil || scope.ActorUserID == "" || scope.ProjectID == "" || scope.View != "draft" && scope.View != "published" {
		return Scope{}, ErrInvalid
	}
	return scope, access.ReadableView(ctx, scope.ActorUserID, scope.ProjectID, scope.View)
}

// chapter resolves an optional chapter key; empty gives nil.
func chapter(version *sagamemory.Version, key string) (*sagamemory.Chapter, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(key) > maxKeyRunes {
		return nil, ErrInvalid
	}
	found, ok := version.Find(key)
	if !ok {
		return nil, ErrInvalid
	}
	return found, nil
}

// book resolves an optional book id; empty gives the first book, nil for an empty version.
func book(version *sagamemory.Version, id string) (*sagamemory.Book, error) {
	books := version.Books()
	id = strings.TrimSpace(id)
	if id == "" {
		if len(books) == 0 {
			return nil, nil
		}
		return &books[0], nil
	}
	for i := range books {
		if books[i].ID == id {
			return &books[i], nil
		}
	}
	return nil, ErrInvalid
}

func validText(text string, limit int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit
}
