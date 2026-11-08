package researchchat

import (
	"context"
	"errors"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/researchchat"
	canonstore "canonkeeper/backend/internal/store/canon"

	"github.com/jackc/pgx/v5"
)

// ReadAuthority resolves the exact running turn bound to the calling root thread.
func (repository *Repository) ReadAuthority(ctx context.Context, call service.ToolContext) (researchdomain.ReadAuthority, error) {
	var authority researchdomain.ReadAuthority
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := queueToolCallInTx(ctx, tx, call); err != nil {
			return err
		}
		var err error
		authority, err = repository.readAuthorityInTx(ctx, tx, call)
		return err
	})
	return authority, mapSourceReadError(err)
}

// RetainChapters records the exact current sources of chapters whose text or derived data
// reached the model in the Job and turn manifests. A chapter whose current revision differs from the
// one the result was built from fails the read: the result is never returned.
func (repository *Repository) RetainChapters(
	ctx context.Context, call service.ToolContext, authority researchdomain.ReadAuthority, chapters []service.ChapterRevision,
) error {
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := queueToolCallInTx(ctx, tx, call); err != nil {
			return err
		}
		if err := repository.confirmReadAuthorityInTx(ctx, tx, call, authority); err != nil {
			return err
		}
		ids := make([]string, len(chapters))
		for index, chapter := range chapters {
			ids[index] = chapter.ChapterID
		}
		current, err := repository.canon.ResolveCurrentChapterRefsInTx(
			ctx, tx, authority.ProjectID, governance.SourceView(authority.SourceView), ids,
		)
		if err != nil {
			return err
		}
		refs := make([]governance.ArtifactSourceRef, 0, len(chapters))
		for _, chapter := range chapters {
			ref, ok := current[chapter.ChapterID]
			if !ok || chapter.Revision < 1 || ref.SourceRevision != uint64(chapter.Revision) ||
				authority.Scope == researchdomain.ScopeBook && ref.BookID != authority.BookID {
				return researchdomain.ErrStaleSource
			}
			refs = append(refs, ref.ExactRef)
		}
		return repository.retainReadSourcesInTx(ctx, tx, call, authority, refs...)
	})
	return mapSourceReadError(err)
}

// queueToolCallInTx makes the transactions of one chat thread wait for each other. Codex 0.164 and
// later run the tool calls of a model step at once; their authority checks share-lock the session,
// project, membership and manifest owners before they lock the Job for update and rewrite the
// manifests, so two of them deadlock. The reading between the two transactions stays parallel.
func queueToolCallInTx(ctx context.Context, tx pgx.Tx, call service.ToolContext) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('research-chat-tool:' || $1::text, 0))`,
		call.Caller.RootThreadID)
	return err
}

func mapSourceReadError(err error) error {
	if errors.Is(err, canonstore.ErrCanonicalSourceNotCurrent) {
		return researchdomain.ErrStaleSource
	}
	return mapError(err)
}
