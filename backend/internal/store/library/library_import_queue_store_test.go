package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
)

func TestLibraryStoreImplementsImportQueueStoreContract(t *testing.T) {
	var _ library.ImportQueueStore = (*Store)(nil)
}

func TestImportQueueDraftPersistsAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "980")
	first := New(db)
	created, err := first.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-create-1", RequestFingerprint: "queue-create-1-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{
			{ClientItemID: "client-1", Position: 1, SourceFileName: "volume-1.fb2", DetectedBookOrder: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}

	second := New(db)
	restored, err := second.GetImportQueueForActor(ctx, library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	if restored.ID != created.ID || restored.Status != library.ImportQueueUploading || restored.Version != 1 {
		t.Fatalf("restored queue = %#v, want id=%q status=%q version=1", restored, created.ID, library.ImportQueueUploading)
	}
	if len(restored.Items) != 1 || restored.Items[0].ClientItemID != "client-1" || restored.Items[0].PreflightStatus != library.ImportQueueItemUploading {
		t.Fatalf("restored items = %#v, want one uploading placeholder", restored.Items)
	}
}

func TestImportQueueRejectsSecondActiveQueueForProject(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "990")
	store := New(db)
	createQueueDraft(t, store, projectID, actorID, "queue-create-1")

	_, err := store.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-create-2", RequestFingerprint: "queue-create-2-fingerprint",
	})
	if !errors.Is(err, library.ErrActiveImportQueueExists) {
		t.Fatalf("second CreateImportQueueDraftForActor() error = %v, want ErrActiveImportQueueExists", err)
	}
}

func TestImportQueueStartsAutomaticSuffixAfterFailedHistory(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b25")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createBookForOrderTest(t, store, projectID, "00000000-0000-4000-8000-000000000b26", "Checkpoint 1", 1, "1", now)
	failed := startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-failed-history", 2)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status = 'failed' WHERE project_id = $1 AND subject_json->>'importQueueId' = $2`, projectID, failed.ID); err != nil {
		t.Fatalf("mark queue jobs failed: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE import_queues SET status = 'failed', completed_at = $2 WHERE id = $1`, failed.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("mark queue failed: %v", err)
	}

	suffix := startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-automatic-suffix", 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
	if suffix.Status != library.ImportQueueRunning || len(suffix.Items) != 11 {
		t.Fatalf("suffix queue = status %q items %d, want running 11", suffix.Status, len(suffix.Items))
	}
}

func TestImportQueueCreateReplayReturnsOriginalQueue(t *testing.T) {
	db, projectID, actorID := newImportQueueStoreFixture(t, "9b0")
	store := New(db)
	first := createQueueDraft(t, store, projectID, actorID, "queue-replay-1")
	second := createQueueDraft(t, store, projectID, actorID, "queue-replay-1")
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("replayed queue = %#v, want original %#v", second, first)
	}
}

func TestImportQueueCreateReplayRejectsFingerprintMismatch(t *testing.T) {
	db, projectID, actorID := newImportQueueStoreFixture(t, "9c0")
	store := New(db)
	createQueueDraft(t, store, projectID, actorID, "queue-fingerprint-1")
	_, err := store.CreateImportQueueDraftForActor(context.Background(), library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID,
		IdempotencyKey: "queue-fingerprint-1", RequestFingerprint: "different-request",
	})
	if !errors.Is(err, library.ErrConflict) {
		t.Fatalf("mismatched replay error = %v, want ErrConflict", err)
	}
}

func TestImportQueueSaveUsesExpectedVersion(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "9a0")
	store := New(db)
	created := createQueueDraft(t, store, projectID, actorID, "queue-create-1")

	saved, err := store.SaveImportQueueDraftForActor(ctx, library.SaveImportQueueDraftStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID, ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("SaveImportQueueDraftForActor() error = %v", err)
	}
	if saved.Version != 2 {
		t.Fatalf("saved version = %d, want 2", saved.Version)
	}
	if _, err := store.SaveImportQueueDraftForActor(ctx, library.SaveImportQueueDraftStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID, ExpectedVersion: 1,
	}); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("stale SaveImportQueueDraftForActor() error = %v, want ErrConflict", err)
	}
}

func TestImportQueueInvalidPreflightStaysUploading(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "9d0")
	store := New(db)
	created, err := store.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-invalid-1", RequestFingerprint: "queue-invalid-1-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{{ClientItemID: "client-invalid", Position: 1, SourceFileName: "bad.fb2"}},
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}
	payload := []byte("invalid payload")
	if _, err := store.PutImportQueueItemForActor(ctx, library.PutImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: created.ID, ItemID: created.Items[0].ID, ActorUserID: actorID,
		ExpectedVersion: created.Version, StorageKey: "queue-invalid-storage", SourceFileName: "bad.fb2",
		MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), SHA256: "invalid-sha", FileBytes: payload,
		PreflightStatus: library.ImportQueueItemInvalid, PreflightErrorJSON: []byte(`{"code":"malformed"}`),
	}); err != nil {
		t.Fatalf("PutImportQueueItemForActor() error = %v", err)
	}
	restored, err := store.GetImportQueueForActor(ctx, library.GetImportQueueStoreInput{ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	if restored.Status != library.ImportQueueUploading {
		t.Fatalf("queue status after invalid preflight = %q, want uploading", restored.Status)
	}
}

func TestImportQueueReplacementScrubsOldUpload(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID := newImportQueueStoreFixture(t, "9e0")
	store := New(db)
	created, err := store.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-replace-1", RequestFingerprint: "queue-replace-1-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{{ClientItemID: "client-replace", Position: 1, SourceFileName: "book.fb2"}},
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}
	payload := []byte("book payload")
	for _, upload := range []struct {
		key     string
		version int64
	}{
		{key: "queue-replace-old", version: created.Version},
		{key: "queue-replace-new", version: created.Version + 1},
	} {
		if _, err := store.PutImportQueueItemForActor(ctx, library.PutImportQueueItemStoreInput{
			ProjectID: projectID, QueueID: created.ID, ItemID: created.Items[0].ID, ActorUserID: actorID,
			ExpectedVersion: upload.version, StorageKey: upload.key, SourceFileName: "book.fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), SHA256: upload.key, FileBytes: payload,
			PreflightStatus: library.ImportQueueItemReady,
		}); err != nil {
			t.Fatalf("PutImportQueueItemForActor(%s) error = %v", upload.key, err)
		}
	}
	var oldPresent, newPresent bool
	if err := db.Pool.QueryRow(ctx, `SELECT content IS NOT NULL FROM import_uploads WHERE project_id = $1 AND storage_key = $2`, projectID, "queue-replace-old").Scan(&oldPresent); err != nil {
		t.Fatalf("read old upload = %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT content IS NOT NULL FROM import_uploads WHERE project_id = $1 AND storage_key = $2`, projectID, "queue-replace-new").Scan(&newPresent); err != nil {
		t.Fatalf("read new upload = %v", err)
	}
	if oldPresent || !newPresent {
		t.Fatalf("upload content presence = old:%t new:%t, want old:false new:true", oldPresent, newPresent)
	}
}

func TestStartImportQueueIsAllOrNothingAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "a10")
	store := New(db)
	queue := readyImportQueue(t, store, projectID, actorID, "queue-start-all", 1, 2, 3)
	input := library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-start-all", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-start-all-fingerprint", SessionWriteProof: sessionProof,
	}
	first, err := store.StartImportQueueForActor(ctx, input)
	if err != nil {
		t.Fatalf("StartImportQueueForActor() error = %v", err)
	}
	replay, err := store.StartImportQueueForActor(ctx, input)
	if err != nil {
		t.Fatalf("StartImportQueueForActor() replay error = %v", err)
	}
	if first.ID != replay.ID || first.Version != replay.Version || first.Status != replay.Status {
		t.Fatalf("replay = %+v, want %+v", replay, first)
	}
	var jobCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE project_id = $1 AND subject_json->>'importQueueId' = $2
	`, projectID, queue.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count queue jobs: %v", err)
	}
	if jobCount != 3 {
		t.Fatalf("queue job count = %d, want 3", jobCount)
	}
	for _, item := range replay.Items {
		if item.ImportJobID == "" {
			t.Fatalf("item %s has no import job", item.ID)
		}
	}
}

func TestStartImportQueueFreezesServerOwnedPublicationPolicy(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b10")
	store := New(db)
	queue := readyImportQueue(t, store, projectID, actorID, "queue-publication-policy", 1)
	started, err := store.StartImportQueueForActor(ctx, library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-publication-policy", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-publication-policy-fingerprint", SessionWriteProof: sessionProof,
	})
	if err != nil {
		t.Fatalf("StartImportQueueForActor() error = %v", err)
	}
	var publication string
	if err := db.Pool.QueryRow(ctx, `
		SELECT initial_publication
		FROM import_job_requests
		WHERE project_id = $1 AND job_id = $2
	`, projectID, started.Items[0].ImportJobID).Scan(&publication); err != nil {
		t.Fatalf("read queue import publication: %v", err)
	}
	if publication != string(library.InitialPublicationPublishImported) {
		t.Fatalf("queue import publication = %q, want %q", publication, library.InitialPublicationPublishImported)
	}
}

func TestStartImportQueueRejectsInvalidDraftWithoutJobs(t *testing.T) {
	tests := []struct {
		name    string
		suffix  string
		orders  []int
		arrange func(*testing.T, *storedb.DB, string, library.ImportQueue)
		want    error
	}{
		{name: "stale version", suffix: "a11", orders: []int{1, 2}, want: library.ErrConflict},
		{name: "order gap", suffix: "a12", orders: []int{1, 3}, want: library.ErrImportSequenceConflict},
		{name: "missing upload", suffix: "a13", orders: []int{1, 2}, arrange: func(t *testing.T, db *storedb.DB, _ string, queue library.ImportQueue) {
			t.Helper()
			if _, err := db.Pool.Exec(context.Background(), `UPDATE import_uploads SET content = NULL WHERE storage_key = $1`, queue.Items[1].StorageKey); err != nil {
				t.Fatalf("remove upload body: %v", err)
			}
		}, want: library.ErrValidation},
		{name: "checksum mismatch", suffix: "a14", orders: []int{1, 2}, arrange: func(t *testing.T, db *storedb.DB, _ string, queue library.ImportQueue) {
			t.Helper()
			if _, err := db.Pool.Exec(context.Background(), `
				UPDATE import_uploads
				SET sha256 = left(sha256, 63) || CASE WHEN right(sha256, 1) = '0' THEN '1' ELSE '0' END
				WHERE storage_key = $1
			`, queue.Items[1].StorageKey); err != nil {
				t.Fatalf("misalign upload checksum: %v", err)
			}
		}, want: library.ErrImportContentMismatch},
		{name: "competing book", suffix: "a15", orders: []int{1, 2}, arrange: func(t *testing.T, db *storedb.DB, projectID string, _ library.ImportQueue) {
			t.Helper()
			insertImportQueueBookForTest(t, db, "00000000-0000-4000-8000-000000000a15", projectID, 2)
		}, want: library.ErrImportExpectedOrderConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, tt.suffix)
			store := New(db)
			queue := readyImportQueue(t, store, projectID, actorID, "queue-"+tt.suffix, tt.orders...)
			if tt.name == "stale version" {
				queue.Version--
			}
			if tt.arrange != nil {
				tt.arrange(t, db, projectID, queue)
			}

			_, err := store.StartImportQueueForActor(ctx, library.StartImportQueueStoreInput{
				ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
				IdempotencyKey: "queue-start-" + tt.suffix, RequestMethod: "POST",
				RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
				RequestFingerprint: "queue-start-" + tt.suffix + "-fingerprint", SessionWriteProof: sessionProof,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("StartImportQueueForActor() error = %v, want %v", err, tt.want)
			}
			var jobCount int
			if err := db.Pool.QueryRow(ctx, `
				SELECT count(*) FROM jobs
				WHERE project_id = $1 AND subject_json->>'importQueueId' = $2
			`, projectID, queue.ID).Scan(&jobCount); err != nil {
				t.Fatalf("count queue jobs: %v", err)
			}
			if jobCount != 0 {
				t.Fatalf("invalid queue created %d jobs, want 0", jobCount)
			}
		})
	}
}

func TestStartImportQueueRollsBackAfterPartialJobCreation(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "a17")
	readyStore := New(db)
	queue := readyImportQueue(t, readyStore, projectID, actorID, "queue-start-rollback", 1, 2, 3)
	failpointErr := errors.New("queue start failpoint")
	jobOwners := 0
	store := New(db, withImportStagingFailpoint(func(point string) error {
		if point != "job.after_owner" {
			return nil
		}
		jobOwners++
		if jobOwners == 3 {
			return failpointErr
		}
		return nil
	}))

	_, err := store.StartImportQueueForActor(ctx, library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-start-rollback", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-start-rollback-fingerprint", SessionWriteProof: sessionProof,
	})
	if !errors.Is(err, failpointErr) {
		t.Fatalf("StartImportQueueForActor() error = %v, want failpoint", err)
	}
	restored, err := readyStore.GetImportQueueForActor(ctx, library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	if restored.Status != library.ImportQueueDraft || restored.Version != queue.Version {
		t.Fatalf("restored queue = %s/%d, want draft/%d", restored.Status, restored.Version, queue.Version)
	}
	var jobCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE subject_json->>'importQueueId' = $1`, queue.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count queue jobs: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("rolled-back queue job count = %d, want 0", jobCount)
	}
}

func TestStartImportQueueReplayRejectsMismatch(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "a16")
	store := New(db)
	queue := readyImportQueue(t, store, projectID, actorID, "queue-start-mismatch", 1)
	input := library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-start-mismatch", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-start-mismatch-fingerprint", SessionWriteProof: sessionProof,
	}
	if _, err := store.StartImportQueueForActor(ctx, input); err != nil {
		t.Fatalf("StartImportQueueForActor() error = %v", err)
	}
	input.IdempotencyKey = "queue-start-other"
	if _, err := store.StartImportQueueForActor(ctx, input); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("mismatched replay error = %v, want %v", err, library.ErrConflict)
	}
	var jobCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE project_id = $1 AND subject_json->>'importQueueId' = $2`, projectID, queue.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count queue jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("queue job count = %d, want 1", jobCount)
	}
}

func createQueueDraft(t *testing.T, store *Store, projectID, actorID, idempotencyKey string) library.ImportQueue {
	t.Helper()
	queue, err := store.CreateImportQueueDraftForActor(context.Background(), library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: idempotencyKey, RequestFingerprint: idempotencyKey + "-fingerprint",
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}
	return queue
}

func readyImportQueue(t *testing.T, store *Store, projectID, actorID, idempotencyKey string, orders ...int) library.ImportQueue {
	t.Helper()
	items := make([]library.CreateImportQueueItemStoreInput, len(orders))
	for index, order := range orders {
		items[index] = library.CreateImportQueueItemStoreInput{
			ClientItemID: fmt.Sprintf("%s-item-%d", idempotencyKey, index), Position: index + 1,
			SourceFileName: fmt.Sprintf("book-%d.fb2", order), DetectedBookOrder: order,
		}
	}
	created, err := store.CreateImportQueueDraftForActor(context.Background(), library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: idempotencyKey,
		RequestFingerprint: idempotencyKey + "-fingerprint", Items: items,
	})
	if err != nil {
		t.Fatalf("CreateImportQueueDraftForActor() error = %v", err)
	}
	for index, item := range created.Items {
		payload := []byte(fmt.Sprintf("queue payload %s %d", idempotencyKey, index))
		digest := sha256.Sum256(payload)
		if _, err := store.PutImportQueueItemForActor(context.Background(), library.PutImportQueueItemStoreInput{
			ProjectID: projectID, QueueID: created.ID, ItemID: item.ID, ActorUserID: actorID,
			ExpectedVersion: created.Version + int64(index), StorageKey: fmt.Sprintf("%s-upload-%d", idempotencyKey, index),
			SourceFileName: item.SourceFileName, MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), FileBytes: payload,
			PreflightStatus: library.ImportQueueItemReady, DetectedBookOrder: orders[index], ConfirmedBookOrder: orders[index],
		}); err != nil {
			t.Fatalf("PutImportQueueItemForActor(%d) error = %v", index, err)
		}
	}
	queue, err := store.GetImportQueueForActor(context.Background(), library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	return queue
}

func insertImportQueueBookForTest(t *testing.T, db *storedb.DB, bookID, projectID string, order int) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		VALUES ($1, $2, $3, $4::integer, $5::text, $6, $6)
	`, bookID, projectID, "Queue Competing Book", order, fmt.Sprint(order), now); err != nil {
		t.Fatalf("insert competing book: %v", err)
	}
}

func newImportQueueStartFixture(t *testing.T, suffix string) (*storedb.DB, string, string, auth.SessionWriteProof) {
	t.Helper()
	db, projectID, actorID := newImportQueueStoreFixture(t, suffix)
	proof := auth.SessionWriteProof{
		SessionHash:    fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+actorID+":session"))),
		CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+actorID+":csrf"))),
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, clock_timestamp() + interval '1 hour', clock_timestamp()
	`, actorID, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatalf("insert start session: %v", err)
	}
	return db, projectID, actorID, proof
}

func newImportQueueStoreFixture(t *testing.T, suffix string) (*storedb.DB, string, string) {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	userID := fmt.Sprintf("00000000-0000-4000-8000-000000000%03s", suffix)
	projectID := fmt.Sprintf("00000000-0000-4000-8000-000000001%03s", suffix)
	membershipID := fmt.Sprintf("00000000-0000-4000-8000-000000002%03s", suffix)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, _ = db.Pool.Exec(ctx, "DELETE FROM import_queues WHERE project_id = $1", projectID)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "import-queue-"+suffix+"@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Queue Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)
	return db, projectID, userID
}
