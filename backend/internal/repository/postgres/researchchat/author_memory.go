package researchchat

import (
	"context"

	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/jackc/pgx/v5"
)

// The memory of the author under the authority of a chat turn (specs/2026-11-01-writer-assistant-index.md,
// UC4): queued behind the other tool calls of the thread, a write for the author of the turn, an editor or
// above.

var _ service.AuthorMemoryRepository = (*Repository)(nil)

// RememberForTurn keeps a record written by the author of the turn.
func (repository *Repository) RememberForTurn(
	ctx context.Context, call service.ToolContext, authority researchdomain.ReadAuthority, draft authormemory.Draft,
) error {
	return repository.toolTx(ctx, call, authority, true, func(tx pgx.Tx) error {
		_, err := authormemoryrepo.CreateInTx(ctx, tx, authority.ProjectID, authority.ActorUserID, draft)
		return err
	})
}

// AuthorMemoryForTurn reads the records of the turn's project.
func (repository *Repository) AuthorMemoryForTurn(
	ctx context.Context, call service.ToolContext, authority researchdomain.ReadAuthority, filter authormemory.Filter,
) (authormemory.Page, error) {
	var page authormemory.Page
	err := repository.toolTx(ctx, call, authority, false, func(tx pgx.Tx) error {
		var err error
		page, err = authormemoryrepo.ListInTx(ctx, tx, authority.ProjectID, filter)
		return err
	})
	return page, err
}

// toolTx runs one transaction of a tool of a turn that reads or writes the author's records: queued behind the other
// tool calls of the thread, under the authority the tool resolved, with an editor or above as the turn's author when
// it writes (researchdomain.ErrNotEditor otherwise).
func (repository *Repository) toolTx(
	ctx context.Context, call service.ToolContext, authority researchdomain.ReadAuthority, writes bool, run func(pgx.Tx) error,
) error {
	return mapSourceReadError(repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := queueToolCallInTx(ctx, tx, call); err != nil {
			return err
		}
		if err := repository.confirmReadAuthorityInTx(ctx, tx, call, authority); err != nil {
			return err
		}
		if writes {
			editor, err := repository.CanWriteInTx(ctx, tx, authority)
			if err != nil {
				return err
			}
			if !editor {
				return researchdomain.ErrNotEditor
			}
		}
		return run(tx)
	}))
}
