package deletion

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func validatePostgresTerminalFence(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
) error {
	if selection.mode == governance.PurgeModeDerivedGeneration {
		return nil
	}
	var sourceTable string
	var present bool
	var err error
	switch {
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionProject:
		sourceTable = "projects"
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM projects
			  WHERE id = $1::uuid AND lifecycle_generation = $2::bigint
			    AND deleted_at IS NOT NULL AND purge_after <= clock_timestamp()
			    AND hard_purge_started_at = $3::timestamptz
			)
		`, selection.projectID, selection.projectGeneration, selection.fencedAt).Scan(&present)
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionBook:
		sourceTable = "books"
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM books
			  WHERE id = $1::uuid AND project_id = $2::uuid
			    AND lifecycle_generation = $3::bigint
			    AND deleted_at IS NOT NULL AND purge_after <= clock_timestamp()
			    AND hard_purge_started_at = $4::timestamptz
			)
		`, selection.bookID, selection.projectID, selection.bookGeneration, selection.fencedAt).Scan(&present)
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionChapter:
		sourceTable = "chapters"
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM chapters
			  WHERE id = $1::uuid AND book_id = $2::uuid AND project_id = $3::uuid
			    AND lifecycle_generation = $4::bigint
			    AND deleted_at IS NOT NULL AND purge_after <= clock_timestamp()
			    AND hard_purge_started_at = $5::timestamptz
			)
		`, selection.chapterID, selection.bookID, selection.projectID,
			selection.chapterGeneration, selection.fencedAt).Scan(&present)
	case selection.mode == governance.PurgeModeChatEpoch && selection.kind == governance.DeletionSelectionChat:
		sourceTable = "chat_sessions"
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM chat_sessions
			  WHERE id = $1::uuid AND project_id = $2::uuid
			    AND deleted_at IS NOT NULL
			    AND lifecycle_epoch = $3::bigint + 1
			)
		`, selection.chatID, selection.projectID, selection.chatEpoch).Scan(&present)
	default:
		return deletionservice.ErrPostgresTargetConflict
	}
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	steps, err := deletion.PostgresStepRegistry(selection.kind, selection.mode)
	if err != nil {
		return deletionservice.ErrPostgresTargetConflict
	}
	stepName := ""
	for _, step := range steps {
		if step.Table == sourceTable && step.Operation == deletion.PostgresOperationDelete {
			stepName = step.Name
			break
		}
	}
	if stepName == "" {
		return deletionservice.ErrPostgresTargetConflict
	}
	var succeeded bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM deletion_target_step_progress
		  WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		    AND step_name = $3 AND state = 'succeeded'
		)
	`, selection.tombstoneID, selection.projectID, stepName).Scan(&succeeded); err != nil {
		return err
	}
	if !succeeded {
		return deletionservice.ErrPostgresTargetConflict
	}
	return nil
}

func verifyPostgresNegativeInventory(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
) error {
	if err := assertPostgresRefsAbsent(ctx, tx, selection); err != nil {
		return err
	}
	var sourceExists bool
	switch {
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionProject:
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1::uuid)`, selection.projectID).Scan(&sourceExists)
		if err != nil {
			return err
		}
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionBook:
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM books WHERE id = $1::uuid AND project_id = $2::uuid)`, selection.bookID, selection.projectID).Scan(&sourceExists)
		if err != nil {
			return err
		}
	case selection.mode == governance.PurgeModeSourceHardPurge && selection.kind == governance.DeletionSelectionChapter:
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chapters WHERE id = $1::uuid AND book_id = $2::uuid AND project_id = $3::uuid)`, selection.chapterID, selection.bookID, selection.projectID).Scan(&sourceExists)
		if err != nil {
			return err
		}
	case selection.mode == governance.PurgeModeChatEpoch && selection.kind == governance.DeletionSelectionChat:
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_sessions WHERE id = $1::uuid AND project_id = $2::uuid)`, selection.chatID, selection.projectID).Scan(&sourceExists)
		if err != nil {
			return err
		}
	}
	if sourceExists {
		return deletionservice.ErrPostgresTargetConflict
	}
	return nil
}

func postgresDeletionSelectionForRequest(request deletion.TargetRequest) (postgresDeletionSelection, error) {
	if request.Claim.Target != governance.TargetPostgreSQL || request.Validate(time.Now().UTC()) != nil {
		return postgresDeletionSelection{}, deletion.ErrInvalidTargetRequest
	}
	fields, err := request.Tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return postgresDeletionSelection{}, deletion.ErrInvalidTargetRequest
	}
	if _, err := deletion.PostgresStepRegistry(fields.Kind, request.Tombstone.Identity.Mode); err != nil {
		return postgresDeletionSelection{}, deletion.ErrInvalidTargetRequest
	}
	selection := postgresDeletionSelection{
		projectID:   request.Claim.ProjectID,
		tombstoneID: request.Claim.TombstoneID,
		kind:        fields.Kind,
		mode:        request.Tombstone.Identity.Mode,
		fencedAt:    request.Tombstone.Identity.FencedAt,
	}
	if fields.Kind == governance.DeletionSelectionChat {
		if uint64(fields.ChatLifecycleEpoch) > math.MaxInt64 {
			return postgresDeletionSelection{}, deletion.ErrInvalidTargetRequest
		}
		selection.chatID = fields.ChatID
		selection.chatEpoch = int64(fields.ChatLifecycleEpoch)
		return selection, nil
	}
	source := fields.Source
	if uint64(source.ProjectGeneration) > math.MaxInt64 ||
		uint64(source.BookGeneration) > math.MaxInt64 ||
		uint64(source.ChapterGeneration) > math.MaxInt64 {
		return postgresDeletionSelection{}, deletion.ErrInvalidTargetRequest
	}
	selection.projectGeneration = int64(source.ProjectGeneration)
	if source.BookID != "" {
		selection.bookID = source.BookID
		selection.bookGeneration = int64(source.BookGeneration)
	}
	if source.ChapterID != "" {
		selection.chapterID = source.ChapterID
		selection.chapterGeneration = int64(source.ChapterGeneration)
	}
	return selection, nil
}

func (selection postgresDeletionSelection) mutationArgs(cursor pgtype.UUID, limit int) []any {
	var cursorArg any
	if cursor.Valid {
		cursorArg = cursor
	}
	return append(selection.predicateArgs(), cursorArg, limit)
}

func (selection postgresDeletionSelection) predicateArgs() []any {
	return []any{
		selection.projectID,
		string(selection.kind),
		selection.projectGeneration,
		nullablePostgresString(selection.bookID),
		nullablePostgresInt64(selection.bookGeneration),
		nullablePostgresString(selection.chapterID),
		nullablePostgresInt64(selection.chapterGeneration),
		nullablePostgresString(selection.chatID),
		nullablePostgresInt64(selection.chatEpoch),
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

func postgresOwnerSelectionPredicate(ownerAlias string) string {
	return fmt.Sprintf(`
		%[1]s.project_id = $1::uuid
		AND %[1]s.read_state IN ('active', 'source_deleted', 'chat_deleted')
		AND EXISTS (
		  SELECT 1 FROM artifact_source_refs ref
		  WHERE ref.owner_id = %[1]s.id AND ref.project_id = %[1]s.project_id
		    AND %s
		)`, ownerAlias, postgresExactRefPredicate("ref"))
}

// postgresExactRefPredicate selects the refs of the deleted source. Book and chapter selections
// skip the refs bound to a chat, so deleting text never purges chat turns, their Jobs or roots
// (specs/2026-10-16-book-deletion-keeps-chats.md R1.2); project and chat selections take all.
func postgresExactRefPredicate(alias string) string {
	return fmt.Sprintf(`
		%[1]s.project_id = $1::uuid
		AND (
		  ($2::text = 'project' AND %[1]s.project_generation = $3::bigint)
		  OR ($2::text = 'book'
		      AND %[1]s.project_generation = $3::bigint
		      AND %[1]s.book_id = $4::uuid AND %[1]s.book_generation = $5::bigint
		      AND %[1]s.chat_session_id IS NULL)
		  OR ($2::text = 'chapter'
		      AND %[1]s.project_generation = $3::bigint
		      AND %[1]s.book_id = $4::uuid AND %[1]s.book_generation = $5::bigint
		      AND %[1]s.chapter_id = $6::uuid AND %[1]s.chapter_generation = $7::bigint
		      AND %[1]s.chat_session_id IS NULL)
		  OR ($2::text = 'chat'
		      AND %[1]s.chat_session_id = $8::uuid AND %[1]s.chat_lifecycle_epoch = $9::bigint)
		)`, alias)
}

func postgresStepCursorDigest(
	selection postgresDeletionSelection,
	step deletion.PostgresStep,
	processedCount int64,
	lastCursor string,
) governance.Digest {
	projection := make([]byte, 0, 160)
	projection = append(projection, "CKPostgresDeletionStepV1\x00"...)
	projection = append(projection, selection.tombstoneID...)
	projection = append(projection, 0)
	projection = append(projection, step.Name...)
	projection = append(projection, 0)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(processedCount))
	projection = append(projection, count[:]...)
	projection = append(projection, lastCursor...)
	return governance.Digest(sha256.Sum256(projection))
}

func mustUUID(value string) pgtype.UUID {
	var result pgtype.UUID
	_ = result.Scan(value)
	return result
}

func nullablePostgresUUID(value string) pgtype.UUID {
	if value == "" {
		return pgtype.UUID{}
	}
	return mustUUID(value)
}

func nullablePostgresInt8(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value != 0}
}
