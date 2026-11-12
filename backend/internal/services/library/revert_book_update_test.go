package library

import (
	"context"
	"errors"
	"strings"
	"testing"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

type revertStoreFake struct{ asked []string }

func (fake *revertStoreFake) PreviewBookUpdateRevert(_ context.Context, _ domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error) {
	fake.asked = append(fake.asked, "preview "+bookID)
	return domainlibrary.BookRevertPreview{BookID: bookID}, nil
}

func (fake *revertStoreFake) RevertBookUpdate(_ context.Context, _ domainlibrary.Actor, bookID, fingerprint string) (string, error) {
	fake.asked = append(fake.asked, "revert "+bookID+" "+fingerprint)
	return "project", nil
}

// specs/2026-11-12-ux10-book-revert.md BR2.2: the return carries the fingerprint of a preview; without an author or a book
// nothing is asked.
func TestARevertCarriesTheFingerprintOfItsPreview(t *testing.T) {
	store := &revertStoreFake{}
	actor := domainlibrary.Actor{UserID: "author"}
	fingerprint := strings.Repeat("0f", 16)
	if _, err := NewRevertBookUpdate(store).Execute(context.Background(), RevertBookUpdateInput{Actor: actor, BookID: "book", Fingerprint: "zz"}); !errors.Is(err, domainlibrary.ErrValidation) {
		t.Fatalf("a fingerprint of another shape = %v", err)
	}
	if _, err := NewRevertBookUpdate(store).Execute(context.Background(), RevertBookUpdateInput{BookID: "book", Fingerprint: fingerprint}); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("no author = %v", err)
	}
	if _, err := NewPreviewBookUpdateRevert(store).Execute(context.Background(), actor, " "); !errors.Is(err, domainlibrary.ErrNotFound) {
		t.Fatalf("no book = %v", err)
	}
	if project, err := NewRevertBookUpdate(store).Execute(context.Background(), RevertBookUpdateInput{Actor: actor, BookID: "book", Fingerprint: fingerprint}); err != nil || project != "project" {
		t.Fatalf("the return = %q, %v", project, err)
	}
	if preview, err := NewPreviewBookUpdateRevert(store).Execute(context.Background(), actor, "book"); err != nil || preview.BookID != "book" {
		t.Fatalf("the preview = %+v, %v", preview, err)
	}
	if strings.Join(store.asked, "; ") != "revert book "+fingerprint+"; preview book" {
		t.Fatalf("asked = %v", store.asked)
	}
}
