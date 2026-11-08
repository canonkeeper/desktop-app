package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/library"
)

func unverifyUserForTest(t *testing.T, ctx context.Context, exec func(context.Context, string, ...any) error, userID string) {
	t.Helper()
	if err := exec(ctx, "UPDATE users SET email_verified_at = NULL WHERE id = $1", userID); err != nil {
		t.Fatal(err)
	}
}

// R1.4.2, R1.4.3: an unverified owner imports no book (new or "update from file")
// and starts no queue; no job, upload or request row appears.
func TestModelStartsRequireVerifiedEmailWithoutWrites(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, sessionProof := newImportQueueStartFixture(t, "c20")
	store := New(db)
	queue := readyImportQueue(t, store, projectID, actorID, "queue-unverified", 1)
	exec := func(ctx context.Context, sql string, args ...any) error {
		_, err := db.Pool.Exec(ctx, sql, args...)
		return err
	}
	unverifyUserForTest(t, ctx, exec, actorID)
	countRows := func(sql string) int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, sql, projectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	jobsBefore := countRows("SELECT count(*) FROM jobs WHERE project_id = $1")
	uploadsBefore := countRows("SELECT count(*) FROM import_uploads WHERE project_id = $1")

	_, err := store.StartImportQueueForActor(ctx, library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-unverified", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-unverified-fingerprint", SessionWriteProof: sessionProof,
	})
	if !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("StartImportQueueForActor() error = %v, want ErrEmailNotVerified", err)
	}

	payload := []byte("<FictionBook>unverified</FictionBook>")
	digest := sha256.Sum256(payload)
	for index, mode := range []library.ImportMode{library.ImportModeNewBook, library.ImportModeUpdateBook} {
		metadata := library.ImportFileMetadata{
			StorageKey: "unverified-" + string(mode), SourceFileName: "book.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)),
			FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: "unverified-" + string(mode),
			ImportMode: mode, InitialPublication: library.InitialPublicationPublishImported,
		}
		if mode == library.ImportModeUpdateBook {
			metadata.TargetBookID = "00000000-0000-4000-8000-00000000c2ff"
		}
		_, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
			JobID: []string{"00000000-0000-4000-8000-00000000c201", "00000000-0000-4000-8000-00000000c202"}[index],
			ProjectID: projectID, ActorUserID: actorID, SessionWriteProof: sessionProof,
			IdempotencyKey: metadata.IdempotencyKey, RequestMethod: "POST",
			RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "unverified-" + string(mode),
			FileBytes: payload, Metadata: metadata,
		})
		if !errors.Is(err, auth.ErrEmailNotVerified) {
			t.Fatalf("CreateImportStagingForActor(%s) error = %v, want ErrEmailNotVerified", mode, err)
		}
	}
	if got := countRows("SELECT count(*) FROM jobs WHERE project_id = $1"); got != jobsBefore {
		t.Fatalf("jobs = %d, want %d", got, jobsBefore)
	}
	if got := countRows("SELECT count(*) FROM import_uploads WHERE project_id = $1"); got != uploadsBefore {
		t.Fatalf("uploads = %d, want %d", got, uploadsBefore)
	}
}
