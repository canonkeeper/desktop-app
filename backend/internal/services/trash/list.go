// Package trash serves listRecentlyDeleted (specs/2026-11-11-ux7-trash.md TB1.1): what the author deleted and may return.
package trash

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/trash"
)

// ErrValidation is a request without an author.
var ErrValidation = errors.New("trash: invalid input")

// Sources lists the deleted sagas, books and chapters the author may return, in every saga of his, newest first.
type Sources interface {
	ListDeletedSources(ctx context.Context, actorUserID string, limit int) (trash.Sources, error)
}

// Drawings lists the deleted drawings the author may return; a build without drawings — the desktop — has none.
type Drawings interface {
	ListDeletedDrawings(ctx context.Context, actorUserID string, limit int) (trash.Group[trash.Drawing], error)
}

// List is «Недавно удалённые»: one list over every saga of the author, by the rules of the returns themselves — a saga
// for its owner or administrator, a book and a chapter of a live saga for an editor and above, a drawing for its author
// while an editor and for the owner.
type List struct {
	Sources  Sources
	Drawings Drawings
}

func (useCase List) Execute(ctx context.Context, actorUserID string) (trash.List, error) {
	if strings.TrimSpace(actorUserID) == "" || useCase.Sources == nil {
		return trash.List{}, ErrValidation
	}
	sources, err := useCase.Sources.ListDeletedSources(ctx, actorUserID, trash.GroupLimit)
	if err != nil {
		return trash.List{}, err
	}
	list := trash.List{Sources: sources}
	if useCase.Drawings != nil {
		drawings, err := useCase.Drawings.ListDeletedDrawings(ctx, actorUserID, trash.GroupLimit)
		if err != nil {
			return trash.List{}, err
		}
		list.Drawings = &drawings
	}
	return list, nil
}
