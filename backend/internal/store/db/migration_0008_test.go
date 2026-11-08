package storedb_test

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/testutil"
	"github.com/jackc/pgx/v5"
)

func TestMigration0008AcceptsAlreadyTerminalAutomaticQueueLineage(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_migration_0008", 7)
	const (
		userID                  = "00000000-0000-4000-8000-00000000e801"
		projectID               = "00000000-0000-4000-8000-00000000e802"
		bookID                  = "00000000-0000-4000-8000-00000000e803"
		runningID               = "00000000-0000-4000-8000-00000000e804"
		terminalID              = "00000000-0000-4000-8000-00000000e805"
		runningItem             = "00000000-0000-4000-8000-00000000e806"
		terminalItem            = "00000000-0000-4000-8000-00000000e807"
		runningImport           = "00000000-0000-4000-8000-00000000e808"
		terminalImport          = "00000000-0000-4000-8000-00000000e809"
		runningEnrichment       = "00000000-0000-4000-8000-00000000e80a"
		terminalEnrichment      = "00000000-0000-4000-8000-00000000e80b"
		runningImportOwner      = "00000000-0000-4000-8000-00000000e80c"
		terminalImportOwner     = "00000000-0000-4000-8000-00000000e80d"
		runningEnrichmentOwner  = "00000000-0000-4000-8000-00000000e80e"
		terminalEnrichmentOwner = "00000000-0000-4000-8000-00000000e80f"
		runningImportRef        = "00000000-0000-4000-8000-00000000e810"
		terminalImportRef       = "00000000-0000-4000-8000-00000000e811"
		runningEnrichmentRef    = "00000000-0000-4000-8000-00000000e812"
		terminalEnrichmentRef   = "00000000-0000-4000-8000-00000000e813"
	)
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id,email,display_name,password_hash,email_verified_at)
		  VALUES ($1,'migration-0008@example.test','Migration','hash',clock_timestamp())`, []any{userID}},
		{`INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at)
		  VALUES ($1,$2,'Migration 0008','',clock_timestamp(),clock_timestamp())`, []any{projectID, userID}},
		{`INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision)
		  VALUES ($1,'draft',1,1)`, []any{projectID}},
		{`INSERT INTO books (id,project_id,title,"order",display_number,status,created_at,updated_at)
		  VALUES ($1,$2,'Ready',1,'1','ready',clock_timestamp(),clock_timestamp())`, []any{bookID, projectID}},
		{`INSERT INTO import_queues (id,project_id,created_by_user_id,idempotency_key,request_fingerprint,status,version,created_at,updated_at,started_at)
		  VALUES ($1,$2,$3,$4,$4,'running',1,clock_timestamp(),clock_timestamp(),clock_timestamp())`,
			[]any{runningID, projectID, userID, "migration-0008-running"}},
		{`INSERT INTO import_queues (id,project_id,created_by_user_id,idempotency_key,request_fingerprint,status,version,created_at,updated_at,started_at,completed_at)
		  VALUES ($1,$2,$3,$4,$4,'succeeded',7,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp())`,
			[]any{terminalID, projectID, userID, "migration-0008-terminal"}},
	}
	for _, statement := range seed {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		queue, item, importJob, ownerID, refID string
	}{
		{runningID, runningItem, runningImport, runningImportOwner, runningImportRef},
		{terminalID, terminalItem, terminalImport, terminalImportOwner, terminalImportRef},
	} {
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			if err := seedMigrationJobArtifact(ctx, tx, projectID, item.importJob, item.ownerID, item.refID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO jobs (
				  id,project_id,kind,status,progress_json,subject_json,creator_user_id,can_cancel,
				  queue_class,retry_count,max_attempts,next_attempt_at,artifact_source_owner_id,
				  governance_payload_state,created_at,updated_at
				) VALUES ($1,$2,'import','succeeded','{}'::jsonb,jsonb_build_object(
				  'type','import','id',$3::text,'projectId',$2::uuid::text
				),$4,true,'user_demanded',0,3,clock_timestamp(),$5,'active',clock_timestamp(),clock_timestamp())
			`, item.importJob, projectID, item.importJob, userID, item.ownerID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO import_queue_items (
				  id,queue_id,project_id,client_item_id,position,source_file_name,
				  detected_book_order,confirmed_book_order,preflight_status,import_job_id,book_id,
				  created_at,updated_at
				) VALUES ($1,$2,$3,$4,1,'volume.fb2',1,1,'ready',$5,$6,clock_timestamp(),clock_timestamp())
			`, item.item, item.queue, projectID, "item-"+item.item, item.importJob, bookID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []struct {
		id, queue, item, ownerID, refID string
	}{
		{runningEnrichment, runningID, runningItem, runningEnrichmentOwner, runningEnrichmentRef},
		{terminalEnrichment, terminalID, terminalItem, terminalEnrichmentOwner, terminalEnrichmentRef},
	} {
		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			if err := seedMigrationJobArtifact(ctx, tx, projectID, job.id, job.ownerID, job.refID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO jobs (
				  id,project_id,kind,status,progress_json,subject_json,can_cancel,
				  queue_class,retry_count,max_attempts,next_attempt_at,artifact_source_owner_id,
				  governance_payload_state,created_at,updated_at
				) VALUES ($1,$2,'canon_book_enrichment','succeeded','{}'::jsonb,jsonb_build_object(
				  'type','canon_book_enrichment','projectId',$2::uuid::text,'sourceView','draft',
				  'bookId',$3::text,'bookManifest',repeat('1',64),'contractVersion','2',
				  'importQueueId',$4::text,'importQueueItemId',$5::text
				),false,'automatic',0,3,clock_timestamp(),$6,'active',clock_timestamp(),clock_timestamp())
			`, job.id, projectID, bookID, job.queue, job.item, job.ownerID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := testutil.ApplyNextMigration(t, db, 7); err != nil {
		t.Fatal(err)
	}
	var runningStatus string
	var runningVersion int64
	var terminalStatus string
	var terminalVersion int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT status FROM import_queues WHERE project_id=$1 AND id=$2),
		  (SELECT version FROM import_queues WHERE project_id=$1 AND id=$2),
		  (SELECT status FROM import_queues WHERE project_id=$1 AND id=$3),
		  (SELECT version FROM import_queues WHERE project_id=$1 AND id=$3)
	`, projectID, runningID, terminalID).Scan(
		&runningStatus, &runningVersion, &terminalStatus, &terminalVersion,
	); err != nil {
		t.Fatal(err)
	}
	if runningStatus != "succeeded" || runningVersion != 2 {
		t.Fatalf("running lineage = %s/%d, want succeeded/2", runningStatus, runningVersion)
	}
	if terminalStatus != "succeeded" || terminalVersion != 7 {
		t.Fatalf("terminal lineage = %s/%d, want unchanged succeeded/7", terminalStatus, terminalVersion)
	}
	var mode, auditState string
	if err := db.Pool.QueryRow(ctx, `
		SELECT progression_mode, audit_state
		FROM import_queues AS queue
		JOIN import_queue_items AS item ON item.queue_id=queue.id AND item.project_id=queue.project_id
		WHERE queue.project_id=$1 AND queue.id=$2
	`, projectID, runningID).Scan(&mode, &auditState); err != nil {
		t.Fatal(err)
	}
	if mode != "automatic" || auditState != "not_required" {
		t.Fatalf("automatic defaults = %s/%s", mode, auditState)
	}
}

func seedMigrationJobArtifact(ctx context.Context, tx pgx.Tx, projectID, jobID, ownerID, refID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO artifact_source_owners (
		  id, project_id, artifact_kind, artifact_id, source_view,
		  manifest_version, manifest_hash, ref_count, read_state, source_identity_state
		) VALUES ($1, $2, 'job', $3, NULL, 1, decode(repeat('11', 32), 'hex'), 1, 'active', 'scope_only')
	`, ownerID, projectID, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO artifact_source_refs (
		  id, owner_id, project_id, ref_kind, scope_kind, source_view,
		  project_generation, source_independent
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true)
	`, refID, ownerID, projectID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE artifact_source_owners AS owner
		SET manifest_hash = (
		  SELECT ck_artifact_manifest_digest(
		    owner.artifact_kind, owner.project_id, owner.artifact_id, owner.source_view,
		    array_agg(ck_artifact_source_ref_projection(
		      ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
		      ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
		      ref.source_view, ref.source_independent,
		      ref.chat_session_id, ref.chat_lifecycle_epoch
		    ))
		  )
		  FROM artifact_source_refs AS ref
		  WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
		)
		WHERE owner.id = $1 AND owner.project_id = $2
	`, ownerID, projectID)
	return err
}
