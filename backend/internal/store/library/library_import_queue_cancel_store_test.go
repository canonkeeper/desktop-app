package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

func TestCancelImportQueueDraftScrubsStagedBytes(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "b30")
	store := New(db)
	queue := readyImportQueue(t, store, projectID, actorID, "queue-cancel-draft", 1, 2)

	canceled, err := store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
	})
	if err != nil {
		t.Fatalf("CancelImportQueueForActor() error = %v", err)
	}
	if canceled.Status != library.ImportQueueCanceled || canceled.Version != queue.Version+1 {
		t.Fatalf("canceled queue = %s/%d, want canceled/%d", canceled.Status, canceled.Version, queue.Version+1)
	}
	for _, item := range canceled.Items {
		var bodyPresent bool
		if err := db.Pool.QueryRow(ctx, `SELECT content IS NOT NULL FROM import_uploads WHERE storage_key = $1`, item.StorageKey).Scan(&bodyPresent); err != nil {
			t.Fatalf("read upload %s: %v", item.StorageKey, err)
		}
		if bodyPresent {
			t.Fatalf("draft upload %s remains present", item.StorageKey)
		}
	}
}

func TestCancelImportQueueUploadingScrubsStagedBytes(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "b33")
	store := New(db)
	created, err := store.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-cancel-uploading",
		RequestFingerprint: "queue-cancel-uploading-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{
			{ClientItemID: "uploaded", Position: 1, SourceFileName: "one.fb2"},
			{ClientItemID: "missing", Position: 2, SourceFileName: "two.fb2"},
		},
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}
	payload := []byte("queued upload bytes")
	digest := sha256.Sum256(payload)
	if _, err := store.PutImportQueueItemForActor(ctx, library.PutImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: created.ID, ItemID: created.Items[0].ID, ActorUserID: actorID,
		ExpectedVersion: created.Version, StorageKey: "queue-cancel-uploading-upload",
		SourceFileName: "one.fb2", MIMEType: "application/x-fictionbook+xml",
		SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), FileBytes: payload,
		PreflightStatus: library.ImportQueueItemInvalid,
	}); err != nil {
		t.Fatalf("PutImportQueueItemForActor() error = %v", err)
	}

	canceled, err := store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID, ExpectedVersion: created.Version + 1,
	})
	if err != nil {
		t.Fatalf("CancelImportQueueForActor() error = %v", err)
	}
	if canceled.Status != library.ImportQueueCanceled {
		t.Fatalf("canceled queue status = %s, want canceled", canceled.Status)
	}
	var bodyPresent bool
	if err := db.Pool.QueryRow(ctx, `SELECT content IS NOT NULL FROM import_uploads WHERE storage_key = $1`, "queue-cancel-uploading-upload").Scan(&bodyPresent); err != nil {
		t.Fatalf("read uploading body: %v", err)
	}
	if bodyPresent {
		t.Fatal("uploading queue body remains present")
	}
}

func TestCancelRunningImportQueueCancelsQueuedJobsAndKeepsRunningWorkerBody(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "b31")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-cancel-running", 1, 2, 3)
	decision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	if decision.Claim.JobID != queue.Items[0].ImportJobID {
		t.Fatalf("claimed job = %s, want %s", decision.Claim.JobID, queue.Items[0].ImportJobID)
	}

	canceled, err := store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
	})
	if err != nil {
		t.Fatalf("CancelImportQueueForActor() error = %v", err)
	}
	if canceled.Status != library.ImportQueueCanceled || canceled.Version != queue.Version+1 {
		t.Fatalf("canceled queue = %s/%d, want canceled/%d", canceled.Status, canceled.Version, queue.Version+1)
	}

	var runningStatus string
	var runningBodyPresent, cancelRequested bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT job.status, cancel_requested_at IS NOT NULL, upload.content IS NOT NULL
		FROM jobs AS job
		JOIN import_queue_items AS item ON item.import_job_id = job.id AND item.project_id = job.project_id
		JOIN import_uploads AS upload ON upload.storage_key = item.storage_key AND upload.project_id = item.project_id
		WHERE job.id = $1
	`, queue.Items[0].ImportJobID).Scan(&runningStatus, &cancelRequested, &runningBodyPresent); err != nil {
		t.Fatalf("read running linked job: %v", err)
	}
	if runningStatus != string(jobs.StatusCanceling) || !cancelRequested || !runningBodyPresent {
		t.Fatalf("running linked job = %s/requested:%t/body:%t, want canceling/true/true", runningStatus, cancelRequested, runningBodyPresent)
	}
	for _, item := range queue.Items[1:] {
		var jobStatus, requestStatus string
		var bodyPresent bool
		if err := db.Pool.QueryRow(ctx, `
			SELECT job.status, request.status, upload.content IS NOT NULL
			FROM jobs AS job
			JOIN import_job_requests AS request ON request.job_id = job.id AND request.project_id = job.project_id
			JOIN import_queue_items AS item ON item.import_job_id = job.id AND item.project_id = job.project_id
			JOIN import_uploads AS upload ON upload.storage_key = item.storage_key AND upload.project_id = item.project_id
			WHERE job.id = $1
		`, item.ImportJobID).Scan(&jobStatus, &requestStatus, &bodyPresent); err != nil {
			t.Fatalf("read queued linked job: %v", err)
		}
		if jobStatus != string(jobs.StatusCanceled) || requestStatus != string(jobs.StatusCanceled) || bodyPresent {
			t.Fatalf("queued linked job = %s/%s/body:%t, want canceled/canceled/false", jobStatus, requestStatus, bodyPresent)
		}
	}
	if _, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("claim after queue cancel error = %v, want exact ErrNotFound", err)
	}
}

func TestCancelFailedImportQueuePreservesSucceededJobAndBook(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "b32")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-cancel-failed", 1, 2)
	decision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	claim := decision.Claim
	if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
		t.Fatalf("MarkImportParsingForClaim() error = %v", err)
	}
	if err := store.MarkImportSavingForClaim(ctx, claim, 1); err != nil {
		t.Fatalf("MarkImportSavingForClaim() error = %v", err)
	}
	finalized, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
		JobID: claim.JobID, ProjectID: projectID, ActorUserID: actorID,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: 1,
		StorageKey: queue.Items[0].StorageKey, BookTitle: "Preserved Queue Book",
		ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
		SourceFileName: queue.Items[0].SourceFileName,
		Chapters:       []library.FinalizeImportChapterInput{{Title: "Chapter", Paragraphs: []library.ParagraphInput{{Markdown: "Text"}}}},
	})
	if err != nil {
		t.Fatalf("FinalizeImport() error = %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE import_queues SET status = 'failed' WHERE id = $1`, queue.ID); err != nil {
		t.Fatalf("mark queue failed: %v", err)
	}

	canceled, err := store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
	})
	if err != nil {
		t.Fatalf("CancelImportQueueForActor() error = %v", err)
	}
	if canceled.Status != library.ImportQueueCanceled {
		t.Fatalf("canceled queue status = %s, want canceled", canceled.Status)
	}
	var jobStatus string
	var bookPresent bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT job.status, book.id IS NOT NULL
		FROM jobs AS job
		JOIN import_queue_items AS item ON item.import_job_id = job.id AND item.project_id = job.project_id
		LEFT JOIN books AS book ON book.id = item.book_id
		WHERE job.id = $1
	`, claim.JobID).Scan(&jobStatus, &bookPresent); err != nil {
		t.Fatalf("read succeeded queue history: %v", err)
	}
	if jobStatus != string(jobs.StatusSucceeded) || !bookPresent || finalized.BookID == "" {
		t.Fatalf("succeeded queue history = %s/book:%t, want succeeded/book:%t", jobStatus, bookPresent, true)
	}
	var queuedStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, queue.Items[1].ImportJobID).Scan(&queuedStatus); err != nil {
		t.Fatalf("read later queue job: %v", err)
	}
	if queuedStatus != string(jobs.StatusCanceled) {
		t.Fatalf("later queue job status = %s, want canceled", queuedStatus)
	}
}

func TestExactIndexingJobResolverBackfillsQueueOriginLineageBeforeFallbackRemoval(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_exact_indexing_job", 3)
	seed := exactResolverSeed{
		userID:    "40000000-0000-4000-8000-000000000001",
		projectID: "40000000-0000-4000-8000-000000000002",
		queueID:   "40000000-0000-4000-8000-000000000003",
		itemID:    "40000000-0000-4000-8000-000000000004",
		bookID:    "40000000-0000-4000-8000-000000000005",
		otherBook: "40000000-0000-4000-8000-000000000006",
		queueJob:  "40000000-0000-4000-8000-000000000011",
		otherJob:  "40000000-0000-4000-8000-000000000012",
		now:       time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	}
	seedExactResolverRows(t, db, seed)
	if err := testutil.ApplyNextMigration(t, db, 3); err != nil {
		t.Fatal(err)
	}
	assertExactResolverBackfill(t, db, seed)
}

type exactResolverSeed struct {
	userID, projectID, queueID, itemID, bookID, otherBook, queueJob, otherJob string
	now                                                                       time.Time
}

func seedExactResolverRows(t *testing.T, db *storedb.DB, seed exactResolverSeed) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exact resolver row: %v", err)
		}
	}
	exec(`INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		      VALUES ($1::uuid, 'exact-resolver@example.test', 'Exact Resolver', 'test-hash', $2::timestamptz)`,
		seed.userID, seed.now)
	exec(`INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		      VALUES ($1::uuid, $2::uuid, 'Exact Resolver', '', $3::timestamptz, $3::timestamptz)`, seed.projectID, seed.userID, seed.now)
	exec(`INSERT INTO import_queues (
	        id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
	        status, version, created_at, updated_at, started_at
		      ) VALUES ($1::uuid,$2::uuid,$3::uuid,'exact-resolver','exact-resolver','running',1,$4::timestamptz,$4::timestamptz,$4::timestamptz)`,
		seed.queueID, seed.projectID, seed.userID, seed.now)
	for _, book := range []string{seed.bookID, seed.otherBook} {
		exec(`INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		      VALUES ($1::uuid,$2::uuid,'Exact Resolver Book',$3::integer,($3::integer)::text,$4::timestamptz,$4::timestamptz)`,
			book, seed.projectID, map[string]int{seed.bookID: 1, seed.otherBook: 2}[book], seed.now)
	}
	exec(`INSERT INTO import_queue_items (
	        id, queue_id, project_id, client_item_id, position, source_file_name,
	        preflight_status, confirmed_book_order, book_id, created_at, updated_at
		      ) VALUES ($1::uuid,$2::uuid,$3::uuid,'exact-resolver',1,'exact.fb2','ready',1,$4::uuid,$5::timestamptz,$5::timestamptz)`,
		seed.itemID, seed.queueID, seed.projectID, seed.bookID, seed.now)

	jobs := []struct {
		id, ownerID, book, manifest string
	}{
		{seed.queueJob, "40000000-0000-4000-8000-000000000021", seed.bookID, strings.Repeat("11", 32)},
		{seed.otherJob, "40000000-0000-4000-8000-000000000022", seed.otherBook, strings.Repeat("22", 32)},
	}
	for _, job := range jobs {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO artifact_source_owners (
		        id, project_id, artifact_kind, artifact_id, manifest_version,
		        manifest_hash, ref_count, read_state, source_identity_state, fenced_at,
		        created_at, updated_at
		      ) VALUES ($1::uuid,$2::uuid,'job',$3::uuid,1,sha256(convert_to(($3::uuid)::text,'UTF8')),1,
		               'source_deleted','scope_only',$4::timestamptz,$5::timestamptz,$5::timestamptz)`,
			job.ownerID, seed.projectID, job.id, seed.now, seed.now); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO artifact_source_refs (
		        id, owner_id, project_id, ref_kind, scope_kind, source_view,
		        project_generation, source_independent
		      ) VALUES (gen_random_uuid(),$1::uuid,$2::uuid,'scope','project',NULL,1,true)`,
			job.ownerID, seed.projectID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE artifact_source_owners AS owner
			SET manifest_hash = (
				SELECT ck_artifact_manifest_digest(
					owner.artifact_kind, owner.project_id, owner.artifact_id, owner.source_view,
					array_agg(ck_artifact_source_ref_projection(
						ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
						ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
						ref.source_view, ref.source_independent,
						ref.chat_session_id, ref.chat_lifecycle_epoch
					)))
				FROM artifact_source_refs AS ref
				WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
			)
			WHERE owner.id = $1::uuid AND owner.project_id = $2::uuid
		`, job.ownerID, seed.projectID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO jobs (
		        id, project_id, kind, status, progress_json, subject_json, result_json,
		        error_json, creator_user_id, can_cancel, queue_class, retry_count,
		        max_attempts, next_attempt_at, artifact_source_owner_id,
		        governance_payload_state, created_at, updated_at
		      ) VALUES (
		        $1::uuid,$2::uuid,'canon_book_enrichment','queued','{"value":0.25}',
		        jsonb_build_object(
		          'type','canon_book_enrichment','projectId',($2::uuid)::text,'sourceView','draft',
		          'bookId',($3::uuid)::text,'bookManifest',$4::text,'contractVersion','2'
		        ),
		        $5::jsonb,NULL,$6::uuid,true,'automatic',0,3,$7::timestamptz,$8::uuid,'active',$7::timestamptz,$7::timestamptz
		      )`,
			job.id, seed.projectID, job.book, job.manifest,
			map[string]string{seed.queueJob: `{"preserve":"queue"}`, seed.otherJob: `{"preserve":"other"}`}[job.id],
			seed.userID, seed.now, job.ownerID)
	}
}

func assertExactResolverBackfill(t *testing.T, db *storedb.DB, seed exactResolverSeed) {
	t.Helper()
	for key, want := range map[string]string{
		"type":              "canon_book_enrichment",
		"projectId":         seed.projectID,
		"sourceView":        "draft",
		"bookId":            seed.bookID,
		"bookManifest":      strings.Repeat("11", 32),
		"contractVersion":   "2",
		"importQueueId":     seed.queueID,
		"importQueueItemId": seed.itemID,
	} {
		var got string
		if err := db.Pool.QueryRow(context.Background(),
			`SELECT subject_json->>$3 FROM jobs WHERE project_id=$1 AND id=$2`,
			seed.projectID, seed.queueJob, key,
		).Scan(&got); err != nil || got != want {
			t.Fatalf("backfilled subject %s = %q err=%v, want %q", key, got, err, want)
		}
	}
	var status, result string
	var createdAt, updatedAt time.Time
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT status, result_json::text, created_at, updated_at
		FROM jobs WHERE project_id=$1 AND id=$2
	`, seed.projectID, seed.queueJob).Scan(&status, &result, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if status != string(jobs.StatusQueued) || result != `{"preserve": "queue"}` ||
		!createdAt.Equal(seed.now) || !updatedAt.Equal(seed.now) {
		t.Fatalf("backfill changed durable job fields: status=%q result=%q created=%s updated=%s",
			status, result, createdAt, updatedAt)
	}
	var otherLineage *string
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT subject_json->>'importQueueId' FROM jobs WHERE project_id=$1 AND id=$2`,
		seed.projectID, seed.otherJob,
	).Scan(&otherLineage); err != nil || otherLineage != nil {
		t.Fatalf("unrelated job lineage = %v err=%v, want absent", otherLineage, err)
	}
}
