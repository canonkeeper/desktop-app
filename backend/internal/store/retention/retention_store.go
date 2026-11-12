package retentionstore

import (
	"bytes"
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	"canonkeeper/backend/internal/retention"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var errStaleRetentionClaim = errors.New("store: stale retention claim")

type Store struct {
	*storedb.DB
	queries *sqlc.Queries
}

func New(db *storedb.DB) *Store {
	if db == nil || db.Pool == nil {
		panic("store/retention: DB is required")
	}
	return &Store{DB: db, queries: sqlc.New(db.Pool)}
}

func (store *Store) RevokeExpiredSessions(ctx context.Context) (int64, error) {
	return store.queries.RevokeExpiredSessions(ctx)
}

func (store *Store) MarkRetentionDeadlineBreaches(ctx context.Context) (int64, error) {
	return store.queries.MarkRetentionDeadlineBreaches(ctx)
}

// RemoveExpiredBookUpdates removes the snapshots of the updates of books from a file whose 7 days are over
// (specs/2026-11-12-ux10-book-revert.md BR1.2); their chapters go with them. No read gives one after its term.
func (store *Store) RemoveExpiredBookUpdates(ctx context.Context) (int64, error) {
	command, err := store.Pool.Exec(ctx, `DELETE FROM book_update_snapshots WHERE purge_after <= clock_timestamp()`)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func (store *Store) GarbageCollectDeletionProofForClaim(
	ctx context.Context,
	claim retention.DeletionAuditGCClaim,
) (bool, error) {
	if store == nil || store.DB == nil || store.Pool == nil || claim.Validate() != nil {
		return false, retention.ErrInvalidClaim
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return false, retention.ErrInvalidClaim
	}
	tombstoneID, err := storedb.ParseUUID(claim.TombstoneID)
	if err != nil {
		return false, retention.ErrInvalidClaim
	}

	deleted := false
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		var lockedID string
		err := tx.QueryRow(ctx, `
			SELECT tombstone.id::text
			FROM deletion_tombstones AS tombstone
			JOIN deletion_completion_receipts AS receipt
			  ON receipt.tombstone_id = tombstone.id
			 AND receipt.project_id = tombstone.project_id
			WHERE tombstone.id = $1
			  AND tombstone.project_id = $2
			  AND tombstone.status = 'purged'
			  AND tombstone.audit_expires_at IS NOT NULL
			  AND tombstone.audit_expires_at <= clock_timestamp()
			  AND receipt.audit_expires_at = tombstone.audit_expires_at
			  AND NOT EXISTS (
			    SELECT 1
			    FROM deletion_hard_purge_predecessors AS predecessor
			    WHERE predecessor.predecessor_tombstone_id = tombstone.id
			      AND predecessor.project_id = tombstone.project_id
			  )
			FOR UPDATE OF tombstone, receipt
		`, tombstoneID, projectID).Scan(&lockedID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `
			DELETE FROM deletion_tombstones AS tombstone
			WHERE tombstone.id = $1
			  AND tombstone.project_id = $2
			  AND tombstone.status = 'purged'
			  AND tombstone.audit_expires_at IS NOT NULL
			  AND tombstone.audit_expires_at <= clock_timestamp()
			  AND NOT EXISTS (
			    SELECT 1
			    FROM deletion_hard_purge_predecessors AS predecessor
			    WHERE predecessor.predecessor_tombstone_id = tombstone.id
			      AND predecessor.project_id = tombstone.project_id
			  )
		`, tombstoneID, projectID)
		if err != nil {
			return err
		}
		deleted = command.RowsAffected() == 1
		return nil
	})
	return deleted, err
}

func (store *Store) EnqueueSourceHardPurgeForClaim(
	ctx context.Context,
	claim retention.SourceHardPurgeClaim,
) (bool, error) {
	if store == nil || store.DB == nil || store.Pool == nil || claim.Validate() != nil {
		return false, retention.ErrInvalidClaim
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return false, retention.ErrInvalidClaim
	}
	entityID, err := storedb.ParseUUID(claim.EntityID)
	if err != nil || (claim.EntityKind == governance.SourceEntityProject && projectID != entityID) {
		return false, retention.ErrInvalidClaim
	}
	claim.ProjectID = projectID.String()
	claim.EntityID = entityID.String()

	applied := false
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(hashtextextended($1::text, 77341001))
		`, claim.ProjectID); err != nil {
			return err
		}
		source, ok, err := lockHardPurgeSource(ctx, tx, claim)
		if err != nil || !ok {
			return err
		}
		if exists, err := sourceHardPurgeExists(ctx, tx, source); err != nil {
			return err
		} else if exists {
			return nil
		}

		var hardStartedAt time.Time
		if err := tx.QueryRow(ctx, `
			SELECT date_trunc('microseconds', clock_timestamp())
		`).Scan(&hardStartedAt); err != nil {
			return err
		}
		hardStartedAt = hardStartedAt.UTC()
		predecessors, ok, err := loadHardPurgePredecessors(ctx, tx, source, hardStartedAt)
		if err != nil || !ok {
			return err
		}
		hardSelection, selectionKind, err := deletionSelectionForSource(source.vector, source.kind)
		if err != nil {
			return err
		}
		predecessorInputs := make([]governance.DeletionPredecessor, 0, len(predecessors))
		for _, predecessor := range predecessors {
			predecessorInputs = append(predecessorInputs, governance.DeletionPredecessor{
				Tombstone:            predecessor.tombstone,
				Status:               governance.DeletionPredecessorPurged,
				ReceiptVectorDigest:  [32]byte(predecessor.vectorDigest),
				TargetReceiptsDigest: [32]byte(predecessor.targetReceiptsDigest),
			})
		}
		predecessorSetDigest, err := governance.DeletionPredecessorSetDigest(hardSelection, predecessorInputs)
		if err != nil {
			return err
		}

		var tombstoneID string
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&tombstoneID); err != nil {
			return err
		}
		if err := insertSourceHardPurgeTombstone(
			ctx, tx, tombstoneID, source, hardStartedAt, predecessorSetDigest,
		); err != nil {
			return err
		}
		for _, predecessor := range predecessors {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_hard_purge_predecessors (
				  hard_tombstone_id, project_id, predecessor_tombstone_id,
				  predecessor_vector_digest, predecessor_tombstone_digest,
				  predecessor_target_receipts_digest
				) VALUES ($1, $2, $3, $4, $5, $6)
			`, tombstoneID, source.vector.ProjectID, predecessor.tombstone.TombstoneID,
				predecessor.vectorDigest[:], predecessor.tombstoneDigest[:],
				predecessor.targetReceiptsDigest[:]); err != nil {
				return err
			}
		}
		for _, target := range deletion.SourceDeletionTargets() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_target_tasks (
				  tombstone_id, project_id, target, state, attempts,
				  claim_incarnation, next_attempt_at
				) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
			`, tombstoneID, source.vector.ProjectID, string(target), hardStartedAt); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(
			ctx, tx, tombstoneID, source.vector.ProjectID,
			selectionKind, governance.PurgeModeSourceHardPurge,
		); err != nil {
			return err
		}
		updated, err := fenceHardPurgeSource(ctx, tx, source, hardStartedAt)
		if err != nil {
			return err
		}
		if !updated {
			return errStaleRetentionClaim
		}
		applied = true
		return nil
	})
	if errors.Is(err, errStaleRetentionClaim) {
		return false, nil
	}
	return applied, err
}

type hardPurgeSource struct {
	kind          governance.SourceEntityKind
	vector        governance.SourceGenerationVector
	purgeDeadline time.Time
}

func lockHardPurgeSource(
	ctx context.Context,
	tx pgx.Tx,
	claim retention.SourceHardPurgeClaim,
) (hardPurgeSource, bool, error) {
	var projectGeneration int64
	switch claim.EntityKind {
	case governance.SourceEntityProject:
		var purgeDeadline time.Time
		err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, purge_deadline
			FROM projects
			WHERE id = $1
			  AND id = $2
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = purge_after + interval '24 hours'
			  AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, claim.ProjectID, claim.EntityID).Scan(&projectGeneration, &purgeDeadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		}
		if err != nil || projectGeneration < 1 {
			return hardPurgeSource{}, false, err
		}
		return hardPurgeSource{
			kind: claim.EntityKind,
			vector: governance.SourceGenerationVector{
				ProjectID:         claim.ProjectID,
				ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			},
			purgeDeadline: purgeDeadline.UTC(),
		}, true, nil
	case governance.SourceEntityBook:
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, claim.ProjectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		} else if err != nil {
			return hardPurgeSource{}, false, err
		}
		var bookGeneration int64
		var purgeDeadline time.Time
		err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, purge_deadline
			FROM books
			WHERE id = $1 AND project_id = $2
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = purge_after + interval '24 hours'
			  AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, claim.EntityID, claim.ProjectID).Scan(&bookGeneration, &purgeDeadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		}
		if err != nil || projectGeneration < 1 || bookGeneration < 1 {
			return hardPurgeSource{}, false, err
		}
		return hardPurgeSource{
			kind: claim.EntityKind,
			vector: governance.SourceGenerationVector{
				ProjectID:         claim.ProjectID,
				ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
				BookID:            claim.EntityID,
				BookGeneration:    governance.LifecycleGeneration(bookGeneration),
			},
			purgeDeadline: purgeDeadline.UTC(),
		}, true, nil
	case governance.SourceEntityChapter:
		var bookID string
		if err := tx.QueryRow(ctx, `
			SELECT book_id::text FROM chapters WHERE id = $1 AND project_id = $2
		`, claim.EntityID, claim.ProjectID).Scan(&bookID); errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		} else if err != nil {
			return hardPurgeSource{}, false, err
		}
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, claim.ProjectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		} else if err != nil {
			return hardPurgeSource{}, false, err
		}
		var bookGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM books
			WHERE id = $1 AND project_id = $2
			  AND deleted_at IS NULL AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, bookID, claim.ProjectID).Scan(&bookGeneration); errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		} else if err != nil {
			return hardPurgeSource{}, false, err
		}
		var chapterGeneration int64
		var purgeDeadline time.Time
		err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, purge_deadline
			FROM chapters
			WHERE id = $1 AND project_id = $2 AND book_id = $3
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = purge_after + interval '24 hours'
			  AND hard_purge_started_at IS NULL
			FOR UPDATE
		`, claim.EntityID, claim.ProjectID, bookID).Scan(&chapterGeneration, &purgeDeadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return hardPurgeSource{}, false, nil
		}
		if err != nil || projectGeneration < 1 || bookGeneration < 1 || chapterGeneration < 1 {
			return hardPurgeSource{}, false, err
		}
		return hardPurgeSource{
			kind: claim.EntityKind,
			vector: governance.SourceGenerationVector{
				ProjectID:         claim.ProjectID,
				ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
				BookID:            bookID,
				BookGeneration:    governance.LifecycleGeneration(bookGeneration),
				ChapterID:         claim.EntityID,
				ChapterGeneration: governance.LifecycleGeneration(chapterGeneration),
			},
			purgeDeadline: purgeDeadline.UTC(),
		}, true, nil
	default:
		return hardPurgeSource{}, false, retention.ErrInvalidClaim
	}
}

func sourceHardPurgeExists(ctx context.Context, tx pgx.Tx, source hardPurgeSource) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM deletion_tombstones AS tombstone
		  WHERE tombstone.project_id = $1
		    AND tombstone.purge_mode = 'source_hard_purge'
		    AND tombstone.entity_kind = $2
		    AND tombstone.entity_id = $3
		    AND tombstone.project_generation = $4
		    AND tombstone.book_id IS NOT DISTINCT FROM $5::uuid
		    AND tombstone.book_generation IS NOT DISTINCT FROM $6::bigint
		    AND tombstone.chapter_id IS NOT DISTINCT FROM $7::uuid
		    AND tombstone.chapter_generation IS NOT DISTINCT FROM $8::bigint
		)
	`, source.vector.ProjectID, string(source.kind), sourceEntityID(source),
		int64(source.vector.ProjectGeneration), nullableUUID(source.vector.BookID),
		nullableGeneration(source.vector.BookGeneration), nullableUUID(source.vector.ChapterID),
		nullableGeneration(source.vector.ChapterGeneration)).Scan(&exists)
	return exists, err
}

type hardPurgePredecessor struct {
	tombstone            governance.DeletionTombstoneInput
	vectorDigest         governance.Digest
	tombstoneDigest      governance.Digest
	targetReceiptsDigest governance.Digest
}

type rawHardPurgePredecessor struct {
	id                        string
	origin                    string
	entityKind                string
	projectID                 string
	permissionLossActorUserID string
	projectGeneration         int64
	bookID                    string
	bookGeneration            int64
	chapterID                 string
	chapterGeneration         int64
	status                    string
	fencedAt                  time.Time
	purgeDeadline             pgtype.Timestamptz
}

func loadHardPurgePredecessors(
	ctx context.Context,
	tx pgx.Tx,
	source hardPurgeSource,
	hardStartedAt time.Time,
) ([]hardPurgePredecessor, bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT
		  tombstone.id::text,
		  tombstone.origin,
		  tombstone.entity_kind,
		  tombstone.project_id::text,
		  coalesce(tombstone.permission_loss_actor_user_id::text, ''),
		  tombstone.project_generation,
		  coalesce(tombstone.book_id::text, ''),
		  coalesce(tombstone.book_generation, 0),
		  coalesce(tombstone.chapter_id::text, ''),
		  coalesce(tombstone.chapter_generation, 0),
		  tombstone.status,
		  tombstone.fenced_at,
		  tombstone.purge_deadline
		FROM deletion_tombstones AS tombstone
		WHERE tombstone.project_id = $1
		  AND tombstone.purge_mode = 'derived_generation'
		  AND (
		    $2::text = 'project'
		    OR ($2::text = 'book'
		      AND tombstone.entity_kind IN ('book', 'chapter')
		      AND tombstone.book_id = $3::uuid)
		    OR ($2::text = 'chapter'
		      AND tombstone.entity_kind = 'chapter'
		      AND tombstone.chapter_id = $3::uuid)
		  )
		ORDER BY uuid_send(tombstone.id)
		FOR UPDATE
	`, source.vector.ProjectID, string(source.kind), sourceEntityID(source))
	if err != nil {
		return nil, false, err
	}
	var raw []rawHardPurgePredecessor
	for rows.Next() {
		var predecessor rawHardPurgePredecessor
		if err := rows.Scan(
			&predecessor.id, &predecessor.origin, &predecessor.entityKind,
			&predecessor.projectID, &predecessor.permissionLossActorUserID, &predecessor.projectGeneration,
			&predecessor.bookID, &predecessor.bookGeneration,
			&predecessor.chapterID, &predecessor.chapterGeneration,
			&predecessor.status, &predecessor.fencedAt, &predecessor.purgeDeadline,
		); err != nil {
			rows.Close()
			return nil, false, err
		}
		raw = append(raw, predecessor)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	if len(raw) == 0 {
		return nil, false, nil
	}

	predecessors := make([]hardPurgePredecessor, 0, len(raw))
	for _, row := range raw {
		predecessor, ok, err := loadHardPurgePredecessor(ctx, tx, row, hardStartedAt)
		if err != nil || !ok {
			return nil, ok, err
		}
		predecessors = append(predecessors, predecessor)
	}
	return predecessors, true, nil
}

func loadHardPurgePredecessor(
	ctx context.Context,
	tx pgx.Tx,
	row rawHardPurgePredecessor,
	hardStartedAt time.Time,
) (hardPurgePredecessor, bool, error) {
	if row.status != "purged" || row.projectGeneration < 1 || row.fencedAt.After(hardStartedAt) {
		return hardPurgePredecessor{}, false, nil
	}
	vector := governance.SourceGenerationVector{
		ProjectID:         row.projectID,
		ProjectGeneration: governance.LifecycleGeneration(row.projectGeneration),
		BookID:            row.bookID,
		BookGeneration:    governance.LifecycleGeneration(row.bookGeneration),
		ChapterID:         row.chapterID,
		ChapterGeneration: governance.LifecycleGeneration(row.chapterGeneration),
	}
	selection, _, err := deletionSelectionForSource(vector, governance.SourceEntityKind(row.entityKind))
	if err != nil {
		return hardPurgePredecessor{}, false, nil
	}
	deadline := governance.AbsentPurgeDeadline()
	if row.purgeDeadline.Valid {
		deadline, err = governance.PresentPurgeDeadline(row.purgeDeadline.Time.UTC())
		if err != nil {
			return hardPurgePredecessor{}, false, nil
		}
	}
	tombstone := governance.DeletionTombstoneInput{
		TombstoneID:               row.id,
		Origin:                    governance.DeletionOrigin(row.origin),
		Mode:                      governance.PurgeModeDerivedGeneration,
		SelectedVector:            selection,
		FencedAt:                  row.fencedAt.UTC(),
		Deadline:                  deadline,
		PermissionLossActorUserID: row.permissionLossActorUserID,
	}
	selectedVectorDigest, err := governance.SelectedVectorDigest(selection)
	if err != nil {
		return hardPurgePredecessor{}, false, nil
	}
	tombstoneDigest, err := governance.DeletionTombstoneDigest(tombstone)
	if err != nil {
		return hardPurgePredecessor{}, false, nil
	}

	var persistedVector, persistedTargetReceipts []byte
	err = tx.QueryRow(ctx, `
		SELECT vector_digest, target_receipts_digest
		FROM deletion_completion_receipts
		WHERE tombstone_id = $1 AND project_id = $2
		FOR UPDATE
	`, row.id, row.projectID).Scan(&persistedVector, &persistedTargetReceipts)
	if errors.Is(err, pgx.ErrNoRows) {
		return hardPurgePredecessor{}, false, nil
	}
	if err != nil {
		return hardPurgePredecessor{}, false, err
	}
	vectorDigest, err := deletionstore.ParseDeletionDigest(persistedVector)
	if err != nil || vectorDigest != governance.Digest(selectedVectorDigest) {
		return hardPurgePredecessor{}, false, nil
	}
	targetReceiptsDigest, err := deletionstore.ParseDeletionDigest(persistedTargetReceipts)
	if err != nil {
		return hardPurgePredecessor{}, false, nil
	}

	taskRows, err := tx.Query(ctx, `
		SELECT target, state, receipt_digest
		FROM deletion_target_tasks
		WHERE tombstone_id = $1 AND project_id = $2
		ORDER BY CASE target
		  WHEN 'runtime' THEN 1
		  WHEN 'filesystem' THEN 2
		  WHEN 'postgresql' THEN 4
		END
		FOR UPDATE
	`, row.id, row.projectID)
	if err != nil {
		return hardPurgePredecessor{}, false, err
	}
	var receipts []governance.DeletionTargetReceiptRow
	validTasks := true
	for taskRows.Next() {
		var target, state string
		var persistedDigest []byte
		if err := taskRows.Scan(&target, &state, &persistedDigest); err != nil {
			taskRows.Close()
			return hardPurgePredecessor{}, false, err
		}
		digest, digestErr := deletionstore.ParseDeletionDigest(persistedDigest)
		if digestErr != nil || (state != "succeeded" && state != "no_data") {
			validTasks = false
			continue
		}
		receipts = append(receipts, governance.DeletionTargetReceiptRow{
			Target: governance.DeletionTarget(target),
			Digest: [32]byte(digest),
		})
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return hardPurgePredecessor{}, false, err
	}
	taskRows.Close()
	aggregate, err := governance.DeletionTargetReceiptsDigest(
		governance.PurgeModeDerivedGeneration,
		tombstoneDigest,
		selectedVectorDigest,
		receipts,
	)
	if err != nil || !validTasks || !bytes.Equal(aggregate[:], targetReceiptsDigest[:]) {
		return hardPurgePredecessor{}, false, nil
	}
	return hardPurgePredecessor{
		tombstone:            tombstone,
		vectorDigest:         vectorDigest,
		tombstoneDigest:      governance.Digest(tombstoneDigest),
		targetReceiptsDigest: targetReceiptsDigest,
	}, true, nil
}

func deletionSelectionForSource(
	vector governance.SourceGenerationVector,
	kind governance.SourceEntityKind,
) (governance.DeletionSelectedVector, governance.DeletionSelectionKind, error) {
	switch kind {
	case governance.SourceEntityProject:
		selection, err := governance.ProjectDeletionVector(vector)
		return selection, governance.DeletionSelectionProject, err
	case governance.SourceEntityBook:
		selection, err := governance.BookDeletionVector(vector)
		return selection, governance.DeletionSelectionBook, err
	case governance.SourceEntityChapter:
		selection, err := governance.ChapterDeletionVector(vector)
		return selection, governance.DeletionSelectionChapter, err
	default:
		return governance.DeletionSelectedVector{}, "", retention.ErrInvalidClaim
	}
}

func insertSourceHardPurgeTombstone(
	ctx context.Context,
	tx pgx.Tx,
	tombstoneID string,
	source hardPurgeSource,
	hardStartedAt time.Time,
	predecessorSetDigest [32]byte,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO deletion_tombstones (
		  id, project_id, origin, entity_kind, purge_mode, entity_id,
		  project_generation, book_id, book_generation,
		  chapter_id, chapter_generation, status, fenced_at,
		  purge_deadline, predecessor_set_digest, created_at, updated_at
		) VALUES (
		  $1, $2, 'governed_online', $3, 'source_hard_purge', $4,
		  $5, $6, $7, $8, $9, 'pending', $10, $11, $12, $10, $10
		)
	`, tombstoneID, source.vector.ProjectID, string(source.kind), sourceEntityID(source),
		int64(source.vector.ProjectGeneration), nullableUUID(source.vector.BookID),
		nullableGeneration(source.vector.BookGeneration), nullableUUID(source.vector.ChapterID),
		nullableGeneration(source.vector.ChapterGeneration), hardStartedAt,
		source.purgeDeadline, predecessorSetDigest[:])
	return err
}

func fenceHardPurgeSource(
	ctx context.Context,
	tx pgx.Tx,
	source hardPurgeSource,
	hardStartedAt time.Time,
) (bool, error) {
	var command pgconn.CommandTag
	var err error
	switch source.kind {
	case governance.SourceEntityProject:
		command, err = tx.Exec(ctx, `
			UPDATE projects
			SET hard_purge_started_at = $2, updated_at = $2
			WHERE id = $1
			  AND lifecycle_generation = $3
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = $4
			  AND hard_purge_started_at IS NULL
		`, source.vector.ProjectID, hardStartedAt,
			int64(source.vector.ProjectGeneration), source.purgeDeadline)
	case governance.SourceEntityBook:
		command, err = tx.Exec(ctx, `
			UPDATE books
			SET hard_purge_started_at = $3, updated_at = $3
			WHERE id = $1 AND project_id = $2
			  AND lifecycle_generation = $4
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = $5
			  AND hard_purge_started_at IS NULL
		`, source.vector.BookID, source.vector.ProjectID, hardStartedAt,
			int64(source.vector.BookGeneration), source.purgeDeadline)
	case governance.SourceEntityChapter:
		command, err = tx.Exec(ctx, `
			UPDATE chapters
			SET hard_purge_started_at = $4, updated_at = $4
			WHERE id = $1 AND project_id = $2 AND book_id = $3
			  AND lifecycle_generation = $5
			  AND deleted_at IS NOT NULL
			  AND governance_retention_started_at IS NOT NULL
			  AND purge_after <= clock_timestamp()
			  AND purge_deadline = $6
			  AND hard_purge_started_at IS NULL
		`, source.vector.ChapterID, source.vector.ProjectID, source.vector.BookID,
			hardStartedAt, int64(source.vector.ChapterGeneration), source.purgeDeadline)
	default:
		return false, retention.ErrInvalidClaim
	}
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func sourceEntityID(source hardPurgeSource) string {
	switch source.kind {
	case governance.SourceEntityBook:
		return source.vector.BookID
	case governance.SourceEntityChapter:
		return source.vector.ChapterID
	default:
		return source.vector.ProjectID
	}
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableGeneration(value governance.LifecycleGeneration) any {
	if value == 0 {
		return nil
	}
	return int64(value)
}
