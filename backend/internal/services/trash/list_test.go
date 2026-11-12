package trash

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/trash"
)

type sourcesStub struct{ limit int }

func (stub *sourcesStub) ListDeletedSources(_ context.Context, _ string, limit int) (trash.Sources, error) {
	stub.limit = limit
	return trash.Sources{Books: trash.Group[trash.Book]{Items: []trash.Book{{ID: "b1"}}, Total: 1}}, nil
}

type drawingsStub struct{}

func (drawingsStub) ListDeletedDrawings(context.Context, string, int) (trash.Group[trash.Drawing], error) {
	return trash.Group[trash.Drawing]{Items: []trash.Drawing{{ID: "d1"}}, Total: 1}, nil
}

// specs/2026-11-11-ux7-trash.md TB1.1: one list of the author's sources and, in a build with drawings, his drawings.
func TestTheListOfRecentlyDeleted(t *testing.T) {
	sources := &sourcesStub{}
	desktop, err := List{Sources: sources}.Execute(context.Background(), "author")
	if err != nil || desktop.Books.Total != 1 || desktop.Drawings != nil || sources.limit != trash.GroupLimit {
		t.Fatalf("a build without drawings = %+v, %v", desktop, err)
	}
	site, err := List{Sources: sources, Drawings: drawingsStub{}}.Execute(context.Background(), "author")
	if err != nil || site.Drawings == nil || site.Drawings.Items[0].ID != "d1" {
		t.Fatalf("a build with drawings = %+v, %v", site, err)
	}
	if _, err := (List{Sources: sources}).Execute(context.Background(), " "); !errors.Is(err, ErrValidation) {
		t.Fatalf("no author = %v", err)
	}
}
