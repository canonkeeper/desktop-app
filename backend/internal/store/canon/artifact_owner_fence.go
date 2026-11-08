package canonstore

import (
	"context"
	"math"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"

	"github.com/jackc/pgx/v5"
)

// artifactOwnerDeletionSelection содержит только границы, которые нужны
// canon-операциям для блокировки и очистки владельцев артефактов.
type artifactOwnerDeletionSelection struct {
	projectID         string
	kind              governance.DeletionSelectionKind
	fencedAt          time.Time
	projectGeneration int64
	bookID            string
	bookGeneration    int64
	chapterID         string
	chapterGeneration int64
}

func (db *Store) RevokeExactSourceArtifactOwnersAtFence(
	ctx context.Context,
	tx pgx.Tx,
	kind governance.DeletionSelectionKind,
	vector governance.SourceGenerationVector,
	fencedAt time.Time,
) error {
	selection, err := exactSourceDeletionSelection(kind, vector, fencedAt)
	if err != nil {
		return err
	}
	return revokeExactArtifactOwnersAtFence(ctx, tx, selection, "source_deleted")
}

func exactSourceDeletionSelection(
	kind governance.DeletionSelectionKind,
	vector governance.SourceGenerationVector,
	fencedAt time.Time,
) (artifactOwnerDeletionSelection, error) {
	var selected governance.DeletionSelectedVector
	var err error
	switch kind {
	case governance.DeletionSelectionProject:
		selected, err = governance.ProjectDeletionVector(vector)
	case governance.DeletionSelectionBook:
		selected, err = governance.BookDeletionVector(vector)
	case governance.DeletionSelectionChapter:
		selected, err = governance.ChapterDeletionVector(vector)
	default:
		return artifactOwnerDeletionSelection{}, deletiondomain.ErrInvalidTargetRequest
	}
	if err != nil {
		return artifactOwnerDeletionSelection{}, err
	}
	fields, err := selected.Fields()
	if err != nil || uint64(fields.Source.ProjectGeneration) > math.MaxInt64 ||
		uint64(fields.Source.BookGeneration) > math.MaxInt64 ||
		uint64(fields.Source.ChapterGeneration) > math.MaxInt64 {
		return artifactOwnerDeletionSelection{}, deletiondomain.ErrInvalidTargetRequest
	}
	return artifactOwnerDeletionSelection{
		projectID:         fields.ProjectID,
		kind:              fields.Kind,
		fencedAt:          fencedAt,
		projectGeneration: int64(fields.Source.ProjectGeneration),
		bookID:            fields.Source.BookID,
		bookGeneration:    int64(fields.Source.BookGeneration),
		chapterID:         fields.Source.ChapterID,
		chapterGeneration: int64(fields.Source.ChapterGeneration),
	}, nil
}

// ScrubCanonicalDerivedSourceInTx drops the derived text data of a deleted source in its fence
// transaction — the text index state, the saga search rows (their words go by cascade), the
// chapter cards, the project's name registry and its thread layer with the checkpoint, which may hold
// names and threads of the deleted source — so none of it outlives the fence, and a restored source is
// indexed and carded again. The next cards Job builds the registry and queues the layer again: a book
// or chapter deletion queues a text index, whose success queues that Job.
func (db *Store) ScrubCanonicalDerivedSourceInTx(
	ctx context.Context,
	tx pgx.Tx,
	kind governance.DeletionSelectionKind,
	vector governance.SourceGenerationVector,
	fencedAt time.Time,
) error {
	selection, err := exactSourceDeletionSelection(kind, vector, fencedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		WITH selected AS (
		  SELECT chapter.id
		  FROM chapters chapter
		  WHERE chapter.project_id = $1::uuid
		    AND (
		      $2::text = 'project'
		      OR ($2::text = 'book' AND chapter.book_id = $3::uuid)
		      OR ($2::text = 'chapter' AND chapter.id = $4::uuid)
		    )
		), scrubbed_state AS (
		  DELETE FROM chapter_text_index_state state
		  USING selected
		  WHERE state.project_id = $1::uuid AND state.chapter_id = selected.id
		), scrubbed_search AS (
		  DELETE FROM chapter_search_texts text
		  USING selected
		  WHERE text.project_id = $1::uuid AND text.chapter_id = selected.id
		), scrubbed_names AS (
		  DELETE FROM saga_names names
		  WHERE names.project_id = $1::uuid
		), scrubbed_threads AS (
		  DELETE FROM saga_threads threads
		  WHERE threads.project_id = $1::uuid
		), scrubbed_map AS (
		  DELETE FROM saga_map_graphs graph
		  WHERE graph.project_id = $1::uuid
		), scrubbed_map_hidden AS (
		  DELETE FROM saga_map_hidden hidden
		  WHERE hidden.project_id = $1::uuid
		)
		DELETE FROM chapter_cards card
		USING selected
		WHERE card.project_id = $1::uuid AND card.chapter_id = selected.id
	`, selection.projectID, string(selection.kind), nullablePostgresString(selection.bookID), nullablePostgresString(selection.chapterID))
	return err
}

// revokeExactArtifactOwnersAtFence fences the owners whose sources the selection deletes. A book
// or chapter deletion leaves the chat artifacts alone: a chat turn is the author's conversation,
// not derived text (specs/2026-10-16-book-deletion-keeps-chats.md R1.1).
func revokeExactArtifactOwnersAtFence(
	ctx context.Context,
	tx pgx.Tx,
	selection artifactOwnerDeletionSelection,
	readState string,
) error {
	args := append(selection.predicateArgs(), selection.fencedAt, readState)
	_, err := tx.Exec(ctx, `
		UPDATE artifact_source_owners owner
		SET read_state = $11::text, fenced_at = $10::timestamptz,
		    updated_at = $10::timestamptz
		WHERE owner.project_id = $1::uuid
		  AND owner.read_state = 'active'
		  AND owner.fenced_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM artifact_source_refs ref
		    WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
		      AND (
		        ($2::text = 'project' AND ref.project_generation = $3::bigint)
		        OR ($2::text = 'book'
		            AND ref.project_generation = $3::bigint
		            AND ref.book_id = $4::uuid AND ref.book_generation = $5::bigint
		            AND ref.chat_session_id IS NULL)
		        OR ($2::text = 'chapter'
		            AND ref.project_generation = $3::bigint
		            AND ref.book_id = $4::uuid AND ref.book_generation = $5::bigint
		            AND ref.chapter_id = $6::uuid AND ref.chapter_generation = $7::bigint
		            AND ref.chat_session_id IS NULL)
		        OR ($2::text = 'chat'
		            AND ref.chat_session_id = $8::uuid AND ref.chat_lifecycle_epoch = $9::bigint)
		      )
		  )
	`, args...)
	return err
}

func (selection artifactOwnerDeletionSelection) predicateArgs() []any {
	return []any{
		selection.projectID,
		string(selection.kind),
		selection.projectGeneration,
		nullablePostgresString(selection.bookID),
		nullablePostgresInt64(selection.bookGeneration),
		nullablePostgresString(selection.chapterID),
		nullablePostgresInt64(selection.chapterGeneration),
		nil,
		nil,
	}
}

func nullablePostgresString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullablePostgresInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func validSourceCatalogView(view governance.SourceView) bool {
	return view == governance.SourceViewDraft || view == governance.SourceViewPublished
}
