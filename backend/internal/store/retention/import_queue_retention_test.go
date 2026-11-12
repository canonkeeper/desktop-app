package retentionstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"

	"github.com/jackc/pgx/v5"
)

func TestRetentionDoesNotInferQueueOutcomeFromLatestJobOrBook(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueRetentionFixture(t, "b40")
	store := librarystore.New(db)
	queue := readyImportQueueForRetention(t, store, projectID, actorID, "queue-retention-success", 1, 2)
	activateImportQueueForRetention(t, db, store, projectID, actorID, queue, proof)

	firstBookID, secondBookID := "00000000-0000-4000-8000-00000000b401", "00000000-0000-4000-8000-00000000b402"
	insertImportQueueRetentionBook(t, db, firstBookID, projectID, 1)
	linkImportQueueRetentionBook(t, db, queue.ID, projectID, 1, firstBookID)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status = 'succeeded', completed_at = clock_timestamp() WHERE project_id = $1 AND kind = 'import'`, projectID); err != nil {
		t.Fatal(err)
	}
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	assertImportQueueRetentionStatus(t, db, queue.ID, "running")

	insertImportQueueRetentionBook(t, db, secondBookID, projectID, 2)
	linkImportQueueRetentionBook(t, db, queue.ID, projectID, 2, secondBookID)
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	assertImportQueueRetentionStatus(t, db, queue.ID, "running")
	// A failed project text index leaves the books unready; retention neither fails nor
	// completes the queue from it.
	insertImportQueueRetentionIndexingJob(t, db, "00000000-0000-4000-8000-00000000b404", projectID, "failed")
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpireStaleImportQueues(ctx); err != nil {
		t.Fatal(err)
	}
	assertImportQueueRetentionStatus(t, db, queue.ID, "running")
}

func TestImportQueueOwnerExpiresStaleUploadingAndDraftQueues(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueRetentionFixture(t, "b41")
	store := librarystore.New(db)
	running := readyImportQueueForRetention(t, store, projectID, actorID, "queue-retention-running", 1)
	activateImportQueueForRetention(t, db, store, projectID, actorID, running, proof)
	if _, err := db.Pool.Exec(ctx, `UPDATE import_uploads SET expires_at = clock_timestamp() - interval '1 second' WHERE project_id = $1`, projectID); err != nil {
		t.Fatal(err)
	}
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	var runningExpires time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT expires_at FROM import_uploads WHERE project_id = $1`, projectID).Scan(&runningExpires); err != nil {
		t.Fatal(err)
	}
	if !runningExpires.After(time.Now().Add(20 * time.Hour)) {
		t.Fatalf("running upload expires at %s, want renewed future time", runningExpires)
	}

	expiredProjectID := newImportQueueRetentionProject(t, db, "b42")
	expiredActorID, expiredProof := newImportQueueRetentionActor(t, db, expiredProjectID, "b42")
	expired := readyImportQueueForRetention(t, store, expiredProjectID, expiredActorID, "queue-retention-expired", 1)
	_ = expiredProof
	if _, err := db.Pool.Exec(ctx, `
		UPDATE import_queues SET created_at = clock_timestamp() - interval '25 hours', updated_at = created_at
		WHERE id = $1
	`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpireStaleImportQueues(ctx); err != nil {
		t.Fatal(err)
	}
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var contentPresent bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT queue.status, upload.content IS NOT NULL
		FROM import_queues AS queue
		JOIN import_queue_items AS item ON item.queue_id = queue.id
		JOIN import_uploads AS upload ON upload.storage_key = item.storage_key
		WHERE queue.id = $1
	`, expired.ID).Scan(&status, &contentPresent); err != nil {
		t.Fatal(err)
	}
	if status != "expired" || contentPresent {
		t.Fatalf("expired draft = %q/body:%t, want expired/false", status, contentPresent)
	}
}

func TestReconcileImportUploadsPreservesAuthoritativeQueueState(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueRetentionFixture(t, "b43")
	store := librarystore.New(db)
	queue := readyImportQueueForRetention(t, store, projectID, actorID, "queue-retention-cancel", 1, 2)
	activateImportQueueForRetention(t, db, store, projectID, actorID, queue, proof)
	bookID := "00000000-0000-4000-8000-00000000b403"
	insertImportQueueRetentionBook(t, db, bookID, projectID, 1)
	linkImportQueueRetentionBook(t, db, queue.ID, projectID, 1, bookID)
	if _, err := db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'canceling', cancel_requested_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = (SELECT import_job_id FROM import_queue_items WHERE queue_id = $1 AND position = 1)
	`, queue.ID); err != nil {
		t.Fatal(err)
	}

	canceled, err := store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != library.ImportQueueCanceled {
		t.Fatalf("canceled status = %q", canceled.Status)
	}
	if err := New(db).ReconcileImportUploads(ctx); err != nil {
		t.Fatal(err)
	}
	var liveBodyPresent, pendingBodyPresent, bookPresent bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  EXISTS (SELECT 1 FROM import_uploads AS upload JOIN import_queue_items AS item ON item.storage_key = upload.storage_key WHERE item.queue_id = $1 AND item.position = 1 AND upload.content IS NOT NULL),
		  EXISTS (SELECT 1 FROM import_uploads AS upload JOIN import_queue_items AS item ON item.storage_key = upload.storage_key WHERE item.queue_id = $1 AND item.position = 2 AND upload.content IS NOT NULL),
		  EXISTS (SELECT 1 FROM books WHERE id = $2)
	`, queue.ID, bookID).Scan(&liveBodyPresent, &pendingBodyPresent, &bookPresent); err != nil {
		t.Fatal(err)
	}
	if !liveBodyPresent || pendingBodyPresent || !bookPresent {
		t.Fatalf("canceled live/pending/book = body:%t/%t book:%t, want true/false/true",
			liveBodyPresent, pendingBodyPresent, bookPresent)
	}
}

func newImportQueueRetentionFixture(t *testing.T, suffix string) (*storedb.DB, string, string, auth.SessionWriteProof) {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	projectID := newImportQueueRetentionProject(t, db, suffix)
	actorID, proof := newImportQueueRetentionActor(t, db, projectID, suffix)
	return db, projectID, actorID, proof
}

func newImportQueueRetentionProject(t *testing.T, db *storedb.DB, suffix string) string {
	t.Helper()
	projectID := fmt.Sprintf("00000000-0000-4000-8000-000000001%s", suffix)
	userID := newImportQueueRetentionUserID(suffix)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, $2, 'Import Queue Retention', 'test-password-hash', clock_timestamp())
		ON CONFLICT (id) DO NOTHING
	`, userID, "import-queue-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, '', clock_timestamp(), clock_timestamp())
	`, projectID, userID, "Import Queue Retention "+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision)
		SELECT id, 'draft', lifecycle_generation, 1 FROM projects WHERE id = $1
	`, projectID); err != nil {
		t.Fatal(err)
	}
	return projectID
}

func newImportQueueRetentionUserID(suffix string) string {
	return fmt.Sprintf("00000000-0000-4000-8000-000000000%s", suffix)
}

func newImportQueueRetentionActor(t *testing.T, db *storedb.DB, projectID, suffix string) (string, auth.SessionWriteProof) {
	t.Helper()
	userID := newImportQueueRetentionUserID(suffix)
	proof := auth.SessionWriteProof{
		SessionHash:    fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":session"))),
		CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":csrf"))),
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'owner', clock_timestamp(), clock_timestamp())
	`, projectID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp() + interval '1 hour', clock_timestamp())
	`, userID, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatal(err)
	}
	return userID, proof
}

func insertImportQueueRetentionBook(t *testing.T, db *storedb.DB, bookID, projectID string, order int) {
	t.Helper()
	displayNumber := fmt.Sprintf("%d", order)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, "order", display_number, status, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3::text, $4::integer, $5::text, 'ready', clock_timestamp(), clock_timestamp())
	`, bookID, projectID, "Import Queue Book "+bookID, order, displayNumber); err != nil {
		t.Fatal(err)
	}
}

func linkImportQueueRetentionBook(t *testing.T, db *storedb.DB, queueID, projectID string, position int, bookID string) {
	t.Helper()
	tag, err := db.Pool.Exec(context.Background(), `
		UPDATE import_queue_items
		SET book_id = $4, updated_at = clock_timestamp()
		WHERE queue_id = $1 AND project_id = $2 AND position = $3 AND book_id IS NULL
	`, queueID, projectID, position, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("link queue item %d affected %d rows, want 1", position, tag.RowsAffected())
	}
}

func insertImportQueueRetentionIndexingJob(t *testing.T, db *storedb.DB, jobID, projectID, status string) {
	t.Helper()
	if err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `
				INSERT INTO artifact_source_owners (
					id, project_id, artifact_kind, artifact_id, source_view,
					manifest_version, manifest_hash, ref_count, read_state, source_identity_state
				)
				VALUES (
					$1, $2, 'job', $3, 'draft', 1,
					decode(repeat('22', 32), 'hex'), 1, 'active', 'scope_only'
				)
			`, "00000000-0000-4000-8000-00000000b405", projectID, jobID); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), `
				INSERT INTO artifact_source_refs (
					id, owner_id, project_id, ref_kind, scope_kind, source_view,
					project_generation, source_independent
				)
				VALUES (
					$1, $2, $3, 'scope', 'project', 'draft', 1, false
				)
		`, "00000000-0000-4000-8000-00000000b406", "00000000-0000-4000-8000-00000000b405", projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), `
			UPDATE artifact_source_owners AS owner
			SET manifest_hash = ck_artifact_manifest_digest(
				'job', owner.project_id, owner.artifact_id, owner.source_view,
				ARRAY(
					SELECT ck_artifact_source_ref_projection(
						ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
						ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
						ref.source_view, ref.source_independent,
						ref.chat_session_id, ref.chat_lifecycle_epoch
					)
					FROM artifact_source_refs AS ref
					WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
				)
			)
			WHERE owner.id = $1::uuid
		`, "00000000-0000-4000-8000-00000000b405"); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO jobs (
					id, project_id, kind, status, subject_json, can_cancel, queue_class,
					retry_count, max_attempts, next_attempt_at, created_at, updated_at,
					artifact_source_owner_id, governance_payload_state
				)
				VALUES (
					$1, $2, 'canon_text_index', $3,
					jsonb_build_object('type', 'canon_text_index', 'projectId', $5::text, 'sourceView', 'draft', 'catalogRevision', 1),
					true, 'user_demanded', 0, 3, clock_timestamp(), clock_timestamp(), clock_timestamp(),
					$4, 'active'
				)
			`, jobID, projectID, status, "00000000-0000-4000-8000-00000000b405", projectID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertImportQueueRetentionStatus(t *testing.T, db *storedb.DB, queueID, want string) {
	t.Helper()
	var got string
	if err := db.Pool.QueryRow(context.Background(), `SELECT status FROM import_queues WHERE id = $1`, queueID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("queue status = %q, want %q", got, want)
	}
}

func readyImportQueueForRetention(t *testing.T, store *librarystore.Store, projectID, actorID, key string, orders ...int) library.ImportQueue {
	t.Helper()
	items := make([]library.CreateImportQueueItemStoreInput, len(orders))
	for index, order := range orders {
		items[index] = library.CreateImportQueueItemStoreInput{
			ClientItemID: fmt.Sprintf("%s-item-%d", key, index), Position: index + 1,
			SourceFileName: fmt.Sprintf("book-%d.fb2", order),
		}
	}
	queue, err := store.CreateImportQueueDraftForActor(context.Background(), library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: key, RequestFingerprint: key + "-fingerprint",
		Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range orders {
		payload := []byte(fmt.Sprintf("%s payload %d", key, index))
		digest := sha256.Sum256(payload)
		_, err := store.PutImportQueueItemForActor(context.Background(), library.PutImportQueueItemStoreInput{
			ProjectID: projectID, QueueID: queue.ID, ItemID: queue.Items[index].ID, ActorUserID: actorID,
			ExpectedVersion: queue.Version + int64(index), StorageKey: key + fmt.Sprintf("-upload-%d", index),
			SourceFileName: queue.Items[index].SourceFileName, MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), FileBytes: payload,
			PreflightStatus: library.ImportQueueItemReady,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	queue, err = store.GetImportQueueForActor(context.Background(), library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func activateImportQueueForRetention(
	t *testing.T, db *storedb.DB, store *librarystore.Store, projectID, actorID string,
	queue library.ImportQueue, proof auth.SessionWriteProof,
) {
	t.Helper()
	ctx := context.Background()
	for _, item := range queue.Items {
		payload := []byte("legacy staging " + item.ID)
		digest := sha256.Sum256(payload)
		_, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
			JobID: item.ID, ProjectID: projectID, ActorUserID: actorID,
			SessionWriteProof: proof, IdempotencyKey: item.ID, RequestMethod: "POST",
			RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: item.ID + "-fingerprint",
			FileBytes: payload,
			Metadata: library.ImportFileMetadata{
				StorageKey: item.ID + "-legacy-upload", SourceFileName: item.SourceFileName,
				Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
				SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]),
				IdempotencyKey: item.ID, ImportMode: library.ImportModeNewBook,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `
			UPDATE import_queue_items
			SET import_job_id = $1::uuid, updated_at = clock_timestamp()
			WHERE id = $1::uuid AND queue_id = $2::uuid AND project_id = $3::uuid
		`, item.ID, queue.ID, projectID); err != nil {
			t.Fatal(err)
		}
	}
	tag, err := db.Pool.Exec(ctx, `
		UPDATE import_queues
		SET status = 'running', version = version + 1,
		    started_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1::uuid AND project_id = $2::uuid AND status = 'draft'
	`, queue.ID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("queue activation affected %d rows, want 1", tag.RowsAffected())
	}
}
