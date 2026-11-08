package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
)

func TestFinalizeImportBackfillsImportQueueItemBookID(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b10")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-finalize-backfill", 1)
	item := queue.Items[0]
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	claim := claimDecision.Claim
	if claim.JobID != item.ImportJobID {
		t.Fatalf("claimed job = %s, want queue item job %s", claim.JobID, item.ImportJobID)
	}
	if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
		t.Fatalf("MarkImportParsingForClaim() error = %v", err)
	}
	if err := store.MarkImportSavingForClaim(ctx, claim, 1); err != nil {
		t.Fatalf("MarkImportSavingForClaim() error = %v", err)
	}

	finalized, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
		JobID: item.ImportJobID, ProjectID: projectID, ActorUserID: actorID,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: 1,
		StorageKey: item.StorageKey, BookTitle: "Queue Imported Book", ImportMode: library.ImportModeNewBook,
		InitialPublication: library.InitialPublicationPublishImported, SourceFileName: item.SourceFileName,
		Chapters: []library.FinalizeImportChapterInput{{Title: "Chapter", Paragraphs: []library.ParagraphInput{{Markdown: "Text"}}}},
	})
	if err != nil {
		t.Fatalf("FinalizeImport() error = %v", err)
	}
	var linkedBookID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT book_id::text FROM import_queue_items
		WHERE project_id = $1 AND id = $2 AND import_job_id = $3
	`, projectID, item.ID, item.ImportJobID).Scan(&linkedBookID); err != nil {
		t.Fatalf("read queue item book link: %v", err)
	}
	if linkedBookID != finalized.BookID {
		t.Fatalf("queue item book_id = %q, want %q", linkedBookID, finalized.BookID)
	}
}

func TestCreateBookRejectsRunningQueueReservedOrder(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b11")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstID := "00000000-0000-4000-8000-000000000b12"
	secondID := "00000000-0000-4000-8000-000000000b13"
	createBookForOrderTest(t, store, projectID, firstID, "First", 1, "1", now)
	createBookForOrderTest(t, store, projectID, secondID, "Second", 2, "2", now)
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-create-reservation", 3)

	_, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000b14", ProjectID: projectID,
		Title: "Manual", Order: 3, DisplayNumber: "3", CreatedAt: now.Add(time.Minute),
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("CreateBook() error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID); got != 2 {
		t.Fatalf("active books after rejected create = %d, want 2", got)
	}
}

func TestCreateBookInsertionRejectsRunningQueueReservation(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b15")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstID := "00000000-0000-4000-8000-000000000b16"
	createBookForOrderTest(t, store, projectID, firstID, "First", 1, "1", now)
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-insert-reservation", 2)

	_, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000b17", ProjectID: projectID,
		Title: "Inserted", Order: 2, DisplayNumber: "2", CreatedAt: now.Add(time.Minute),
		AfterBookID: library.OrderPlacement{Present: true, AfterID: &firstID},
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("CreateBook(insertion) error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID); got != 1 {
		t.Fatalf("active books after rejected insertion = %d, want 1", got)
	}
}

func TestUpdateBookReorderRejectsRunningQueueReservation(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b18")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstID := "00000000-0000-4000-8000-000000000b19"
	secondID := "00000000-0000-4000-8000-000000000b1a"
	createBookForOrderTest(t, store, projectID, firstID, "First", 1, "1", now)
	createBookForOrderTest(t, store, projectID, secondID, "Second", 2, "2", now)
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-reorder-reservation", 3)

	_, err := store.UpdateBook(ctx, library.UpdateBookStoreInput{
		ProjectID: projectID, BookID: secondID, Title: "Second",
		UpdatedAt: now.Add(time.Minute), AfterBookID: library.OrderPlacement{Present: true},
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("UpdateBook(reorder) error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	var firstOrder, secondOrder int
	if err := db.Pool.QueryRow(ctx, `SELECT "order" FROM books WHERE id = $1`, firstID).Scan(&firstOrder); err != nil {
		t.Fatalf("read first book order: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT "order" FROM books WHERE id = $1`, secondID).Scan(&secondOrder); err != nil {
		t.Fatalf("read second book order: %v", err)
	}
	if firstOrder != 1 || secondOrder != 2 {
		t.Fatalf("book orders after rejected reorder = %d,%d, want 1,2", firstOrder, secondOrder)
	}
}

func TestBookDeletionRejectsRunningQueueReservation(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b1b")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstID := "00000000-0000-4000-8000-000000000b1c"
	secondID := "00000000-0000-4000-8000-000000000b1d"
	createBookForOrderTest(t, store, projectID, firstID, "First", 1, "1", now)
	createBookForOrderTest(t, store, projectID, secondID, "Second", 2, "2", now)
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-delete-reservation", 3)

	_, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: firstID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: now.Add(time.Minute),
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("FenceBookDeletion() error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID); got != 2 {
		t.Fatalf("active books after rejected deletion = %d, want 2", got)
	}
}

func TestBookRestoreRejectsRunningQueueReservedOrder(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b1e")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	bookID := "00000000-0000-4000-8000-000000000b1f"
	createBookForOrderTest(t, store, projectID, bookID, "Deleted", 1, "1", now)
	if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("FenceBookDeletion(before queue) error = %v", err)
	}
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-restore-reservation", 1)

	_, err := store.RestoreBook(ctx, library.RestoreBookStoreInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: bookID,
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("RestoreBook() error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	var stillDeleted bool
	if err := db.Pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&stillDeleted); err != nil {
		t.Fatalf("read restored book state: %v", err)
	}
	if !stillDeleted {
		t.Fatal("rejected restore changed book to active")
	}
}

func TestImportStagingRejectsRunningQueueReservation(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b20")
	store := New(db)
	startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-import-reservation", 1)
	payload := []byte("<FictionBook>competing import</FictionBook>")
	digest := sha256.Sum256(payload)

	_, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
		JobID: "00000000-0000-4000-8000-000000000b21", ProjectID: projectID, ActorUserID: actorID,
		SessionWriteProof: sessionProof, IdempotencyKey: "queue-competing-import",
		RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
		RequestFingerprint: "queue-competing-import-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: "queue-competing-import-upload", SourceFileName: "competing.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)),
			FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: "queue-competing-import",
			ImportMode: library.ImportModeNewBook,
		},
	})
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("CreateImportStagingForActor() error = %v, want %v", err, library.ErrImportExpectedOrderConflict)
	}
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM jobs WHERE project_id = $1`, projectID); got != 1 {
		t.Fatalf("jobs after rejected staging = %d, want only queue job", got)
	}
}

func TestCreateBookAllowsFailedQueueOrder(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "b22")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createBookForOrderTest(t, store, projectID, "00000000-0000-4000-8000-000000000b23", "First", 1, "1", now)
	queue := startReadyImportQueue(t, store, projectID, actorID, sessionProof, "queue-failed-reservation", 2)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status = 'failed' WHERE project_id = $1 AND subject_json->>'importQueueId' = $2`, projectID, queue.ID); err != nil {
		t.Fatalf("mark queue jobs failed: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE import_queues SET status = 'failed' WHERE id = $1`, queue.ID); err != nil {
		t.Fatalf("mark queue failed: %v", err)
	}

	_, err := store.CreateBook(ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000b24", ProjectID: projectID,
		Title: "Manual", Order: 2, DisplayNumber: "2", CreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateBook() error = %v", err)
	}
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID); got != 2 {
		t.Fatalf("active books after create = %d, want 2", got)
	}
}

func startReadyImportQueue(t *testing.T, store *Store, projectID, actorID string, sessionProof auth.SessionWriteProof, idempotencyKey string, orders ...int) library.ImportQueue {
	t.Helper()
	queue := readyImportQueue(t, store, projectID, actorID, idempotencyKey, orders...)
	started, err := store.StartImportQueueForActor(context.Background(), library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: idempotencyKey + "-start", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: idempotencyKey + "-start-fingerprint",
		SessionWriteProof:  sessionProof,
	})
	if err != nil {
		t.Fatalf("StartImportQueueForActor() error = %v", err)
	}
	return started
}
