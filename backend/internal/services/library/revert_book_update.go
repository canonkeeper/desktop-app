package library

import (
	"context"
	"encoding/hex"
	"strings"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

// BookRevertStore reads and writes the return of a book to the version before its last update from a file under the
// guards of a write: the actor edits the saga, the saga is not the demo one, the book is not being updated, its snapshot
// lives.
type BookRevertStore interface {
	PreviewBookUpdateRevert(ctx context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error)
	RevertBookUpdate(ctx context.Context, actor domainlibrary.Actor, bookID, fingerprint string) (string, error)
}

// PreviewBookUpdateRevert is previewBookUpdateRevert (specs/2026-11-12-ux10-book-revert.md BR2.1): what «Вернуть прежнюю
// версию» would do to the book now. It changes nothing.
type PreviewBookUpdateRevert struct {
	store BookRevertStore
}

func NewPreviewBookUpdateRevert(store BookRevertStore) *PreviewBookUpdateRevert {
	if store == nil {
		panic("book revert preview: store is required")
	}
	return &PreviewBookUpdateRevert{store: store}
}

func (useCase *PreviewBookUpdateRevert) Execute(ctx context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error) {
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(bookID) == "" {
		return domainlibrary.BookRevertPreview{}, domainlibrary.ErrNotFound
	}
	return useCase.store.PreviewBookUpdateRevert(ctx, actor, bookID)
}

// RevertBookUpdateInput names the book and the fingerprint of the preview the author agreed to.
type RevertBookUpdateInput struct {
	Actor       domainlibrary.Actor
	BookID      string
	Fingerprint string
}

// RevertBookUpdate is revertBookUpdate (BR2.2, BR2.3): the book comes back to the version before its last update from a
// file, as its preview said, in one transaction, or nothing changes. It is not an import: no Job, no upload of the day.
type RevertBookUpdate struct {
	store BookRevertStore
}

func NewRevertBookUpdate(store BookRevertStore) *RevertBookUpdate {
	if store == nil {
		panic("book revert: store is required")
	}
	return &RevertBookUpdate{store: store}
}

// Execute returns the book and reports its saga.
func (useCase *RevertBookUpdate) Execute(ctx context.Context, input RevertBookUpdateInput) (string, error) {
	if strings.TrimSpace(input.Actor.UserID) == "" || strings.TrimSpace(input.BookID) == "" {
		return "", domainlibrary.ErrNotFound
	}
	if fingerprint, err := hex.DecodeString(input.Fingerprint); err != nil || len(fingerprint) != 16 {
		return "", domainlibrary.ErrValidation
	}
	return useCase.store.RevertBookUpdate(ctx, input.Actor, input.BookID, input.Fingerprint)
}
