package indexingstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	"canonkeeper/backend/internal/retention"
	storedb "canonkeeper/backend/internal/store/db"
	retentionstore "canonkeeper/backend/internal/store/retention"

	"github.com/jackc/pgx/v5"
)

func softDeleteIndexingStoreChapterForTest(t *testing.T, db *storedb.DB, chapterID string) {
	softDeleteIndexingStoreChapterAtForTest(t, db, chapterID, time.Now().UTC().Truncate(time.Microsecond))
}

func softDeleteIndexingStoreChapterAtForTest(t *testing.T, db *storedb.DB, chapterID string, deletedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		var projectID, bookID string
		var projectGeneration, bookGeneration, chapterGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT chapter.project_id::text, chapter.book_id::text,
			       project.lifecycle_generation, book.lifecycle_generation, chapter.lifecycle_generation
			FROM chapters AS chapter
			JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id
			JOIN projects AS project ON project.id = chapter.project_id
			WHERE chapter.id = $1
			  AND chapter.deleted_at IS NULL AND book.deleted_at IS NULL AND project.deleted_at IS NULL
			FOR UPDATE OF project, book, chapter
		`, chapterID).Scan(&projectID, &bookID, &projectGeneration, &bookGeneration, &chapterGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, book_id, book_generation, chapter_id, chapter_generation,
				status, fenced_at, purge_deadline
			) VALUES (
				$1, $2, 'governed_online', 'chapter', 'derived_generation', $3,
				$4, $5, $6, $3, $7, 'pending', $8, $9
			)
		`, tombstoneID, projectID, chapterID, projectGeneration, bookID, bookGeneration,
			chapterGeneration, deletedAt, deletedAt.Add(deletion.DerivedPurgeDeadline)); err != nil {
			return err
		}
		for _, target := range deletion.SourceDeletionTargets() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_target_tasks (
					tombstone_id, project_id, target, state, attempts,
					claim_incarnation, next_attempt_at
				) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
			`, tombstoneID, projectID, string(target), deletedAt); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(
			ctx, tx, tombstoneID, projectID,
			governance.DeletionSelectionChapter, governance.PurgeModeDerivedGeneration,
		); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE chapters
			SET deleted_at = $1,
			    governance_retention_started_at = $1,
			    purge_after = $1::timestamptz + interval '7 days',
			    purge_deadline = $1::timestamptz + interval '8 days',
			    lifecycle_generation = $2 + 1
			WHERE id = $3 AND lifecycle_generation = $2
		`, deletedAt, chapterGeneration, chapterID)
		return err
	})
	if err != nil {
		t.Fatalf("soft delete chapter: %v", err)
	}
}

func TestChapterHardPurgeDeletesChapterSearchRows(t *testing.T) {
	fixture := newStoreFixture(t, 7311, governance.SourceViewDraft)
	snapshot := fixture.snapshot(t)
	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1 AND chapter_id=$2
	`, fixture.projectID, fixture.chapterID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("chapter search rows = %d, want 1", rows)
	}

	softDeleteIndexingStoreChapterAtForTest(
		t, fixture.db, fixture.chapterID, fixture.now.Add(-9*24*time.Hour),
	)
	err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `SET LOCAL session_replication_role=replica`); err != nil {
			return err
		}
		completedAt := fixture.now.Add(-8 * 24 * time.Hour)
		if _, err := tx.Exec(context.Background(), `
			UPDATE deletion_tombstones
			SET status='purged', purged_at=$3
			WHERE project_id=$1 AND purge_mode='derived_generation' AND fenced_at=$2
		`, fixture.projectID, fixture.now.Add(-9*24*time.Hour), completedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), `
			UPDATE deletion_target_tasks
			SET state='succeeded', attempts=1, claim_incarnation=1,
			    next_attempt_at=$3, claimed_by_digest=decode(repeat('31',32),'hex'),
			    claim_expires_at=$3,
			    handler_version=CASE target
			      WHEN 'runtime' THEN 'runtime_direct_sdk_v1'
			      WHEN 'filesystem' THEN 'filesystem_os_root_v1'
			      ELSE 'postgres_exact_inventory_v1' END,
			    receipt_digest=CASE target
			      WHEN 'runtime' THEN decode(repeat('13',32),'hex')
			      WHEN 'filesystem' THEN decode(repeat('23',32),'hex')
			      ELSE decode(repeat('43',32),'hex') END,
			    completed_at=$3
			WHERE project_id=$1 AND tombstone_id IN (
			  SELECT id FROM deletion_tombstones
			  WHERE project_id=$1 AND purge_mode='derived_generation' AND fenced_at=$2
			)
		`, fixture.projectID, fixture.now.Add(-9*24*time.Hour), completedAt); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `
			INSERT INTO deletion_completion_receipts (
			  tombstone_id, project_id, vector_digest, target_receipts_digest,
			  completed_at, audit_expires_at, schema_version
			)
			SELECT tombstone.id, tombstone.project_id,
			  ck_deletion_selected_vector_digest(
			    tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			    tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			    tombstone.chapter_generation, tombstone.chat_session_id,
			    tombstone.chat_lifecycle_epoch),
			  ck_deletion_target_receipt_set_digest(
			    tombstone.purge_mode,
			    ck_deletion_tombstone_digest(
			      tombstone.id, tombstone.origin, tombstone.purge_mode, tombstone.entity_kind,
			      tombstone.project_id, tombstone.project_generation, tombstone.book_id,
			      tombstone.book_generation, tombstone.chapter_id, tombstone.chapter_generation,
			      tombstone.chat_session_id, tombstone.chat_lifecycle_epoch,
			      tombstone.fenced_at, tombstone.purge_deadline),
			    ck_deletion_selected_vector_digest(
			      tombstone.entity_kind, tombstone.project_id, tombstone.project_generation,
			      tombstone.book_id, tombstone.book_generation, tombstone.chapter_id,
			      tombstone.chapter_generation, tombstone.chat_session_id,
			      tombstone.chat_lifecycle_epoch),
			    ARRAY[
			      decode(repeat('13',32),'hex'), decode(repeat('23',32),'hex'),
			      decode(repeat('43',32),'hex')]),
			  $3, NULL, 'CKDeletionReceiptV1'
			FROM deletion_tombstones AS tombstone
			WHERE tombstone.project_id=$1
			  AND tombstone.purge_mode='derived_generation'
			  AND tombstone.fenced_at=$2
		`, fixture.projectID, fixture.now.Add(-9*24*time.Hour), completedAt)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := retentionstore.New(fixture.db).EnqueueSourceHardPurgeForClaim(context.Background(), retention.SourceHardPurgeClaim{
		ProjectID: fixture.projectID, EntityKind: governance.SourceEntityChapter, EntityID: fixture.chapterID,
	})
	if err != nil || !applied {
		t.Fatalf("enqueue source hard purge = %t, %v", applied, err)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		WITH hard AS (
		  SELECT id FROM deletion_tombstones
		  WHERE project_id=$1 AND purge_mode='source_hard_purge'
		), terminal AS (
		  UPDATE deletion_target_tasks
		  SET state='succeeded', attempts=1, claim_incarnation=1,
		      claimed_by_digest=decode(repeat('31',32),'hex'),
		      claim_expires_at=date_trunc('second', clock_timestamp()) + interval '5 minutes',
		      handler_version=CASE target
		        WHEN 'runtime' THEN 'runtime_direct_sdk_v1'
		        ELSE 'filesystem_os_root_v1' END,
		      receipt_digest=CASE target
		        WHEN 'runtime' THEN decode(repeat('13',32),'hex')
		        ELSE decode(repeat('23',32),'hex') END,
		      completed_at=date_trunc('second', clock_timestamp())
		  WHERE tombstone_id IN (SELECT id FROM hard) AND target <> 'postgresql'
		), processing AS (
		  UPDATE deletion_tombstones
		  SET status='processing', updated_at=clock_timestamp()
		  WHERE id IN (SELECT id FROM hard)
		), chapter_step AS (
		  SELECT step.tombstone_id, step.project_id, step.step_order
		  FROM deletion_target_step_progress AS step
		  JOIN hard ON hard.id = step.tombstone_id
		  WHERE step.step_name LIKE '%:chapters:delete'
		), earlier AS (
		  UPDATE deletion_target_step_progress AS step
		  SET state='succeeded', claim_incarnation=1,
		      claimed_by_digest=decode(repeat('31',32),'hex'),
		      cursor_digest=decode(repeat('51',32),'hex'),
		      started_at=date_trunc('second', clock_timestamp()),
		      updated_at=date_trunc('second', clock_timestamp()),
		      completed_at=date_trunc('second', clock_timestamp())
		  FROM chapter_step
		  WHERE step.tombstone_id=chapter_step.tombstone_id
		    AND step.project_id=chapter_step.project_id
		    AND step.step_order < chapter_step.step_order
		), claimed AS (
		  UPDATE deletion_target_tasks
		  SET state='processing', attempts=1, claim_incarnation=1,
		      claimed_by_digest=decode(repeat('31',32),'hex'),
		      claim_expires_at=clock_timestamp() + interval '5 minutes'
		  WHERE tombstone_id IN (SELECT id FROM hard) AND target='postgresql'
		  RETURNING tombstone_id, project_id
		)
		UPDATE deletion_target_step_progress AS step
		SET state='running', claim_incarnation=1,
		    claimed_by_digest=decode(repeat('31',32),'hex'),
		    started_at=clock_timestamp(), updated_at=clock_timestamp(),
		    completed_at=NULL, cursor_digest=NULL
		FROM claimed
		WHERE step.tombstone_id=claimed.tombstone_id
		  AND step.project_id=claimed.project_id
		  AND step.step_name LIKE '%:chapters:delete'
	`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		DELETE FROM chapter_paragraphs WHERE chapter_id=$1
	`, fixture.chapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		DELETE FROM chapters WHERE project_id=$1 AND id=$2
	`, fixture.projectID, fixture.chapterID); err != nil {
		t.Fatal(err)
	}

	var retained int
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1 AND chapter_id=$2
	`, fixture.projectID, fixture.chapterID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 0 {
		t.Fatalf("hard-purged chapter search rows = %d, want 0", retained)
	}
}
