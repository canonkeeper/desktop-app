package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	indexingstore "canonkeeper/backend/internal/store/indexing"
)

// specs/2026-11-11-ux7-upload.md UP3.2: a volume that did not read waits for the author at its place; the volumes after it
// go on by themselves and stand at their own places — the queue is not stopped, and no volume is confirmed by hand.
func TestAVolumeThatDidNotReadWaitsAtItsPlaceAndTheNextOnesGoOn(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d01")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-waiting-d01", 1, 2, 3)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)

	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[0], 1)

	second, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || second.Claim.JobID != queue.Items[1].ImportJobID {
		t.Fatalf("second claim = %+v, %v; want volume 2", second.Claim, err)
	}
	if committed, err := store.FinalizeImportTerminalForClaim(ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: second.Claim, Status: jobs.StatusFailed,
		Error: &jobs.Error{Code: "invalid_import", Message: "volume 2 failed"},
	}); err != nil || committed != jobs.StatusFailed {
		t.Fatalf("FinalizeImportTerminalForClaim() = %s, %v", committed, err)
	}
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueRunning {
		t.Fatalf("queue after a volume did not read = %s, want running: the volume waits for the author", status)
	}

	// The third volume goes on and stands at its own place, after the place the second one keeps.
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[2], 3)
	if order := bookOrderOfQueueItemForTest(t, store, projectID, actorID, queue.ID, 2); order != 3 {
		t.Fatalf("third volume stands at order %d, want 3", order)
	}
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueRunning {
		t.Fatalf("queue with a volume waiting = %s, want running", status)
	}
	if _, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("claim with only the waiting volume left = %v, want ErrNotFound", err)
	}
}

// UP2.3 and the finding of ux7-reorder (11.11): in a saga where a book in the middle was deleted, a queue of two volumes
// starts and its volumes stand after the books of the shelf, one after another — the place the deleted book keeps is not
// «taken» by the queue and no live book stands in its way.
func TestAQueueAfterADeletedBookInTheMiddleStandsAfterTheBooks(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d02")
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{"00000000-0000-4000-8000-000000000d21", "00000000-0000-4000-8000-000000000d22", "00000000-0000-4000-8000-000000000d23"}
	for index, id := range ids {
		createBookForOrderTest(t, store, projectID, id, fmt.Sprintf("Книга %d", index+1), index+1, fmt.Sprint(index+1), now)
	}
	if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: actorID, ProjectID: projectID, BookID: ids[1],
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	if next, err := store.NextBookOrder(ctx, projectID); err != nil || next != 4 {
		t.Fatalf("NextBookOrder() = %d, %v; want 4 — after the books of the shelf", next, err)
	}

	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-after-deleted-d02", 1, 2)
	for index, item := range queue.Items {
		if item.ConfirmedBookOrder != 4+index {
			t.Fatalf("volume %d stands at order %d, want %d", index+1, item.ConfirmedBookOrder, 4+index)
		}
	}
}

// UP3.2: «Заменить файл» gives the waiting volume a new file at its place; the queue takes it by itself, the book stands
// between the volumes around it, and the queue ends once it is read. The new file is one more upload.
func TestReplacingTheFileOfAWaitingVolumePutsItAtItsPlace(t *testing.T) {
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d03")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-replace-d03", 1, 2, 3)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[0], 1)
	failVolumeForTest(t, store, claims, queue.Items[1])
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[2], 3)

	replaced := replaceVolumeFileForTest(t, store, projectID, actorID, proof, queue.ID, queue.Items[1].ID, "replace-d03", "tom-2-fixed.fb2")
	item := replaced.Items[1]
	if replaced.Status != library.ImportQueueRunning || item.ImportJobID == queue.Items[1].ImportJobID ||
		item.ImportJobStatus != string(jobs.StatusQueued) || item.SourceFileName != "tom-2-fixed.fb2" || item.ConfirmedBookOrder != 2 {
		t.Fatalf("replaced volume = %+v in a %s queue, want a new queued Job at order 2", item, replaced.Status)
	}
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, item, 2)
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueSucceeded {
		t.Fatalf("queue after the replaced volume is read = %s, want succeeded", status)
	}
	if shelf := shelfOrderForTest(t, store, projectID); shelf != "Том 1:1 Том 2:2 Том 3:3" {
		t.Fatalf("the shelf = %s, want the replaced volume between the others", shelf)
	}
	if uploads := uploadsOfTheDay(t, db, actorID); uploads != 4 {
		t.Fatalf("uploads = %d, want 4: three volumes and the new file", uploads)
	}
}

// UP3.2: «Пропустить» takes the waiting volume out of the upload; the queue ends once the others are read, and a queue
// whose every volume is skipped ends as stopped.
func TestSkippingAWaitingVolumeEndsTheUpload(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d04")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-skip-d04", 1, 2)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	failVolumeForTest(t, store, claims, queue.Items[0])
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[1], 2)
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueRunning {
		t.Fatalf("queue with the first volume waiting = %s, want running", status)
	}
	skipped, err := store.SkipImportQueueItemForActor(ctx, library.SkipImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("SkipImportQueueItemForActor() error = %v", err)
	}
	if skipped.Status != library.ImportQueueSucceeded || skipped.Items[0].SkippedAt == nil {
		t.Fatalf("queue after the skip = %s, first volume skipped at %v; want succeeded and skipped", skipped.Status, skipped.Items[0].SkippedAt)
	}

	single := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-skip-one-d04", 1)
	failVolumeForTest(t, store, claims, single.Items[0])
	alone, err := store.SkipImportQueueItemForActor(ctx, library.SkipImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: single.ID, ItemID: single.Items[0].ID, ActorUserID: actorID,
	})
	if err != nil || alone.Status != library.ImportQueueCanceled {
		t.Fatalf("queue of one skipped volume = %s, %v; want canceled", alone.Status, err)
	}
}

// UP3.2: only a volume that did not read takes a new file or is skipped: one in work or read, or one skipped already, is
// a conflict, and its queue does not change.
func TestOnlyAWaitingVolumeTakesANewFileOrIsSkipped(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d05")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-guard-d05", 1, 2)
	claims := newWorkerClaimAuthorityForTest(db)
	if _, err := store.SkipImportQueueItemForActor(ctx, library.SkipImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: actorID,
	}); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("skip of a queued volume = %v, want ErrConflict", err)
	}
	if _, err := store.ReplaceImportQueueItemFileForActor(ctx, replaceInputForTest(projectID, actorID, proof, queue.ID, queue.Items[0].ID, "guard-d05", "other.fb2")); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("new file of a queued volume = %v, want ErrConflict", err)
	}
	failVolumeForTest(t, store, claims, queue.Items[0])
	if _, err := store.SkipImportQueueItemForActor(ctx, library.SkipImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: actorID,
	}); err != nil {
		t.Fatalf("skip of the waiting volume = %v", err)
	}
	if _, err := store.SkipImportQueueItemForActor(ctx, library.SkipImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: actorID,
	}); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("second skip = %v, want ErrConflict", err)
	}
	if _, err := store.ReplaceImportQueueItemFileForActor(ctx, replaceInputForTest(projectID, actorID, proof, queue.ID, queue.Items[0].ID, "guard-skipped-d05", "late.fb2")); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("new file of a skipped volume = %v, want ErrConflict", err)
	}
}

// UP1.2, UP3.2: a file the server's check does not pass does not hold the files that pass: the queue starts, the file
// waits at its place with its reason and holds no volume after it back, counts no book and no upload until it gets a
// file that passes, and «Заменить файл» gives it one.
func TestAFileThatDoesNotPassTheCheckWaitsAtItsPlaceAndTheOthersGo(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "d06")
	store := New(db)
	created, err := store.CreateImportQueueDraftForActor(ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: projectID, ActorUserID: actorID, IdempotencyKey: "queue-invalid-d06", RequestFingerprint: "queue-invalid-d06-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{
			{ClientItemID: "d06-1", Position: 1, SourceFileName: "tom-1.fb2"},
			{ClientItemID: "d06-2", Position: 2, SourceFileName: "tom-2.epub"},
			{ClientItemID: "d06-3", Position: 3, SourceFileName: "tom-3.fb2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range created.Items {
		payload := []byte(fmt.Sprintf("volume %d", index+1))
		digest := sha256.Sum256(payload)
		input := library.PutImportQueueItemStoreInput{
			ProjectID: projectID, QueueID: created.ID, ItemID: item.ID, ActorUserID: actorID,
			ExpectedVersion: created.Version + int64(index), StorageKey: fmt.Sprintf("d06-upload-%d", index),
			SourceFileName: item.SourceFileName, MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), FileBytes: payload,
			PreflightStatus: library.ImportQueueItemReady,
		}
		if index == 1 {
			input.MIMEType, input.PreflightStatus = "application/epub+zip", library.ImportQueueItemInvalid
			input.PreflightErrorJSON = []byte(`{"code":"invalid_epub_container","message":"EPUB container is invalid."}`)
		}
		if _, err := store.PutImportQueueItemForActor(ctx, input); err != nil {
			t.Fatalf("PutImportQueueItemForActor(%d) error = %v", index+1, err)
		}
	}
	received, err := store.GetImportQueueForActor(ctx, library.GetImportQueueStoreInput{ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID})
	if err != nil || received.Status != library.ImportQueueDraft {
		t.Fatalf("queue with every file received = %s, %v; want draft", received.Status, err)
	}
	queue, err := store.StartImportQueueForActor(ctx, library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: created.ID, ActorUserID: actorID, ExpectedVersion: received.Version,
		IdempotencyKey: "queue-invalid-d06-start", RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/import-queues/" + created.ID + "/start", RequestFingerprint: "d06-start",
		SessionWriteProof: proof,
	})
	if err != nil {
		t.Fatalf("StartImportQueueForActor() error = %v", err)
	}
	if queue.Items[1].ImportJobID != "" || queue.Items[1].ConfirmedBookOrder != 2 || queue.Items[0].ImportJobID == "" || queue.Items[2].ImportJobID == "" {
		t.Fatalf("started volumes = %+v, want Jobs for the files that passed and the place 2 kept", queue.Items)
	}
	if uploads := uploadsOfTheDay(t, db, actorID); uploads != 2 {
		t.Fatalf("uploads at the start = %d, want 2: the file that did not pass counts nothing", uploads)
	}
	var invalidBody bool
	if err := db.Pool.QueryRow(ctx, `SELECT content IS NOT NULL FROM import_uploads WHERE project_id = $1 AND storage_key = 'd06-upload-1'`, projectID).Scan(&invalidBody); err != nil || invalidBody {
		t.Fatalf("the file that did not pass is kept = %t, %v; want it gone at the start", invalidBody, err)
	}
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[0], 1)
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, queue.Items[2], 3)
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueRunning {
		t.Fatalf("queue with the file waiting = %s, want running", status)
	}
	replaced := replaceVolumeFileForTest(t, store, projectID, actorID, proof, queue.ID, queue.Items[1].ID, "replace-d06", "tom-2.fb2")
	importVolumeForTest(t, store, claims, indexing, projectID, actorID, replaced.Items[1], 2)
	if shelf := shelfOrderForTest(t, store, projectID); shelf != "Том 1:1 Том 2:2 Том 3:3" {
		t.Fatalf("the shelf = %s", shelf)
	}
	if status := importQueueStatusForTest(t, store, projectID, actorID, queue.ID); status != library.ImportQueueSucceeded {
		t.Fatalf("queue after the replaced file is read = %s, want succeeded", status)
	}
}

func failVolumeForTest(t *testing.T, store *Store, claims *workerClaimAuthorityForLibraryTest, item library.ImportQueueItem) {
	t.Helper()
	ctx := context.Background()
	decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != item.ImportJobID {
		t.Fatalf("claim of the volume to fail = %+v, %v; want job %s", decision.Claim, err, item.ImportJobID)
	}
	if committed, err := store.FinalizeImportTerminalForClaim(ctx, library.FinalizeImportTerminalForClaimInput{
		Claim: decision.Claim, Status: jobs.StatusFailed, Error: &jobs.Error{Code: "invalid_import", Message: "the volume did not read"},
	}); err != nil || committed != jobs.StatusFailed {
		t.Fatalf("FinalizeImportTerminalForClaim() = %s, %v", committed, err)
	}
}

func replaceInputForTest(projectID, actorID string, proof auth.SessionWriteProof, queueID, itemID, key, fileName string) library.ReplaceImportQueueItemStoreInput {
	payload := []byte("replaced volume " + key)
	digest := sha256.Sum256(payload)
	return library.ReplaceImportQueueItemStoreInput{
		ProjectID: projectID, QueueID: queueID, ItemID: itemID, ActorUserID: actorID,
		StorageKey: key + "-upload", SourceFileName: fileName, MIMEType: "application/x-fictionbook+xml",
		SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]), FileBytes: payload,
		PreflightStatus: library.ImportQueueItemReady, IdempotencyKey: key, RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/import-queues/" + queueID + "/items", RequestFingerprint: key + "-fingerprint",
		SessionWriteProof: proof,
	}
}

func replaceVolumeFileForTest(t *testing.T, store *Store, projectID, actorID string, proof auth.SessionWriteProof, queueID, itemID, key, fileName string) library.ImportQueue {
	t.Helper()
	queue, err := store.ReplaceImportQueueItemFileForActor(context.Background(), replaceInputForTest(projectID, actorID, proof, queueID, itemID, key, fileName))
	if err != nil {
		t.Fatalf("ReplaceImportQueueItemFileForActor() error = %v", err)
	}
	return queue
}

// shelfOrderForTest is the live books of the saga as «title:order» in the order of the shelf.
func shelfOrderForTest(t *testing.T, store *Store, projectID string) string {
	t.Helper()
	rows, err := store.Pool.Query(context.Background(), `
		SELECT title || ':' || "order" FROM books WHERE project_id = $1 AND deleted_at IS NULL ORDER BY "order", id
	`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var shelf []string
	for rows.Next() {
		var place string
		if err := rows.Scan(&place); err != nil {
			t.Fatal(err)
		}
		shelf = append(shelf, place)
	}
	return strings.Join(shelf, " ")
}

func importVolumeForTest(t *testing.T, store *Store, claims *workerClaimAuthorityForLibraryTest, indexing *indexingstore.Store, projectID, actorID string, item library.ImportQueueItem, volume int) {
	t.Helper()
	ctx := context.Background()
	decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != item.ImportJobID {
		t.Fatalf("claim of volume %d = %+v, %v; want job %s", volume, decision.Claim, err, item.ImportJobID)
	}
	claim := decision.Claim
	if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkImportSavingForClaim(ctx, claim, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
		JobID: claim.JobID, ProjectID: projectID, ActorUserID: actorID,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: item.ConfirmedBookOrder,
		StorageKey: item.StorageKey, BookTitle: fmt.Sprintf("Том %d", volume),
		ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
		SourceFileName: item.SourceFileName,
		Chapters:       []library.FinalizeImportChapterInput{{Title: "Глава", Paragraphs: []library.ParagraphInput{{Markdown: "Текст"}}}},
	}); err != nil {
		t.Fatalf("FinalizeImport(volume %d) error = %v", volume, err)
	}
	textClaim, textSubject := claimDraftTextIndexForTest(t, store.DB, projectID)
	indexCurrentTextForTest(t, indexing, textClaim)
	if err := indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
		t.Fatal(err)
	}
}

func importQueueStatusForTest(t *testing.T, store *Store, projectID, actorID, queueID string) library.ImportQueueStatus {
	t.Helper()
	queue, err := store.GetImportQueueForActor(context.Background(), library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: queueID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	return queue.Status
}

// bookOrderOfQueueItemForTest is the stored order of the book of the queue item at index.
func bookOrderOfQueueItemForTest(t *testing.T, store *Store, projectID, actorID, queueID string, index int) int {
	t.Helper()
	queue, err := store.GetImportQueueForActor(context.Background(), library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: queueID, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatalf("GetImportQueueForActor() error = %v", err)
	}
	var order int
	if err := store.Pool.QueryRow(context.Background(), `SELECT "order" FROM books WHERE id = $1`, queue.Items[index].BookID).Scan(&order); err != nil {
		t.Fatalf("read book order of item %d: %v", index+1, err)
	}
	return order
}
