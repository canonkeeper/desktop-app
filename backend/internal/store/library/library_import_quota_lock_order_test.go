package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5/pgconn"
)

const lockOrderSagaB = "00000000-0000-4000-8000-000000000a81"

// runningImportForTest stages and claims an import of the fixture's actor in saga A: the Job is
// running and was created today, like an import that is about to finish.
func (fixture *quotaImportFixture) runningImportForTest(key, jobID string) {
	fixture.t.Helper()
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	if _, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.actorID, SessionWriteProof: fixture.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		fixture.t.Fatalf("stage %s: %v", key, err)
	}
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != jobID {
		fixture.t.Fatalf("claim %s = %+v, %v", key, decision.Claim, err)
	}
}

// waitForAdvisoryWaiter returns once a session of the test database waits for an advisory lock.
func (fixture *quotaImportFixture) waitForAdvisoryWaiter() {
	fixture.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'
		`).Scan(&waiting); err != nil {
			fixture.t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fixture.t.Fatal("the upload never waited for the owner's quota lock")
}

func deadlocked(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// specs/2026-10-28-owner-tools-and-counters.md, lock order: an import of U in saga A finishes —
// the owner's quota lock (CheckOwnerSourcesInTx), then the terminal status whose trigger takes
// U's day row — while U uploads a book or starts a queue in saga B of the same owner. Every path
// takes the owner's quota lock before a writer_usage_days row, so neither waits in a cycle.
func TestImportFinishingAndUploadInAnotherSagaDoNotDeadlock(t *testing.T) {
	for _, upload := range []struct {
		name  string
		start func(fixture *quotaImportFixture) error
	}{
		{"single upload", func(fixture *quotaImportFixture) error {
			payload := []byte("<FictionBook>saga-b</FictionBook>")
			digest := sha256.Sum256(payload)
			_, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
				JobID: "00000000-0000-4000-8000-000000000b91", ProjectID: lockOrderSagaB, ActorUserID: fixture.actorID,
				SessionWriteProof: fixture.proof, IdempotencyKey: "saga-b", RequestMethod: "POST",
				RequestPath: "/projects/" + lockOrderSagaB + "/imports", RequestFingerprint: "saga-b-fingerprint", FileBytes: payload,
				Metadata: library.ImportFileMetadata{
					StorageKey: "saga-b", SourceFileName: "saga-b.fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
					SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: "saga-b",
					ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
				},
			})
			return err
		}},
		{"queue start", func(fixture *quotaImportFixture) error {
			queue := readyImportQueue(fixture.t, fixture.store, lockOrderSagaB, fixture.actorID, "saga-b-queue", 1, 2)
			_, err := fixture.store.StartImportQueueForActor(fixture.ctx, library.StartImportQueueStoreInput{
				ProjectID: lockOrderSagaB, QueueID: queue.ID, ActorUserID: fixture.actorID, ExpectedVersion: queue.Version,
				IdempotencyKey: "saga-b-queue", RequestMethod: "POST",
				RequestPath:        "/projects/" + lockOrderSagaB + "/import-queues/" + queue.ID + "/start",
				RequestFingerprint: "saga-b-queue-fingerprint", SessionWriteProof: fixture.proof,
			})
			return err
		}},
	} {
		t.Run(upload.name, func(t *testing.T) {
			fixture := newQuotaImportFixture(t, false)
			now := time.Now().UTC().Truncate(time.Microsecond)
			insertImportStoreProjectForTest(t, fixture.db, lockOrderSagaB, fixture.ownerID, "Saga B", now)
			insertProjectStoreMembershipForTest(t, fixture.db, "00000000-0000-4000-8000-000000000a82", lockOrderSagaB, fixture.ownerID, projects.RoleOwner, now, false)
			const finishing = "00000000-0000-4000-8000-000000000b90"
			fixture.runningImportForTest("saga-a", finishing)

			finish, err := fixture.db.Pool.Begin(fixture.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer finish.Rollback(context.Background())
			if err := quotastore.CheckOwnerSourcesInTx(fixture.ctx, finish, fixture.projectID); err != nil {
				t.Fatal(err)
			}
			uploaded := make(chan error, 1)
			go func() { uploaded <- upload.start(fixture) }()
			fixture.waitForAdvisoryWaiter()
			_, finishErr := finish.Exec(fixture.ctx, `UPDATE jobs SET status = 'failed' WHERE id = $1`, finishing)
			if finishErr == nil {
				finishErr = finish.Commit(fixture.ctx)
			} else {
				_ = finish.Rollback(fixture.ctx)
			}
			var uploadErr error
			select {
			case uploadErr = <-uploaded:
			case <-time.After(30 * time.Second):
				t.Fatal("the upload did not finish")
			}
			if deadlocked(finishErr) || deadlocked(uploadErr) || finishErr != nil || uploadErr != nil {
				t.Fatalf("finishing import = %v, upload in saga B = %v", finishErr, uploadErr)
			}
			if failed := fixture.count(`SELECT COALESCE(sum(imports_failed), 0) FROM writer_usage_days WHERE user_id = $1`, fixture.actorID); failed != 1 {
				t.Fatalf("failed imports counted = %d, want 1", failed)
			}
		})
	}
}
