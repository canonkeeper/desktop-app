package librarystore

import (
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/bookexport"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-11-import-speed.md IS1.4: the write of a whole book by batches is refused as the write by rows was, and
// a refused write leaves nothing of the book — no book, chapter, paragraph of either text version or member of the
// catalog — and the Job running: a book over the text of the tariff, a lost claim, a cancelled Job, a deleted saga.
func TestARefusedWriteOfABookLeavesNoRowsOfIt(t *testing.T) {
	refusedBy := func(want error) func(error) bool { return func(err error) bool { return errors.Is(err, want) } }
	for name, refusal := range map[string]struct {
		refuse  func(*importWriteFixture, *library.FinalizeImportStoreInput) error
		refused func(error) bool
	}{
		"over the text of the tariff": {func(fixture *importWriteFixture, _ *library.FinalizeImportStoreInput) error {
			limit := int64(1000)
			return fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				_, err := quotastore.SetOverridesInTx(fixture.ctx, tx, fixture.userID, []quota.OverrideChange{
					{Key: quota.KeyProjectCharacters, Value: &limit},
				}, time.Now())
				return err
			})
		}, func(err error) bool {
			exceeded, ok := quota.AsExceeded(err)
			return ok && exceeded.Key == quota.KeyProjectCharacters
		}},
		"with a lost claim": {func(_ *importWriteFixture, input *library.FinalizeImportStoreInput) error {
			input.ExpectedClaimIncarnation++
			return nil
		}, refusedBy(library.ErrConflict)},
		"of a cancelled Job": {func(fixture *importWriteFixture, input *library.FinalizeImportStoreInput) error {
			_, err := jobstore.New(fixture.db).CancelJobStatus(fixture.ctx, jobs.CancelJobStatusStoreInput{
				ActorUserID: fixture.userID, ProjectID: fixture.projectID, JobID: input.JobID,
				From: jobs.StatusRunning, To: jobs.StatusCanceling, UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
			})
			return err
		}, refusedBy(library.ErrConflict)},
		"into a deleted saga": {func(fixture *importWriteFixture, _ *library.FinalizeImportStoreInput) error {
			return deleteProjectForLibraryTest(fixture.ctx, fixture.db, fixture.projectID, time.Now().Add(time.Second))
		}, refusedBy(library.ErrNotFound)},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newImportWriteFixture(t, openCurrentStoreTestDB(t))
			input := fixture.claimedImport(largeBookFile(t, 17, 1006))
			if err := refusal.refuse(fixture, &input); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.FinalizeImport(fixture.ctx, input); !refusal.refused(err) {
				t.Fatalf("the write %s = %v; want it refused as the write by rows was", name, err)
			}
			assertImportTreeCounts(t, fixture.db, fixture.projectID, 0, 0, 0)
			for _, table := range []string{"book_source_members", "chapter_source_members"} {
				var rows int
				if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM `+table+` WHERE project_id = $1`, fixture.projectID).Scan(&rows); err != nil {
					t.Fatal(err)
				}
				if rows != 0 {
					t.Fatalf("the write %s left %d rows of %s", name, rows, table)
				}
			}
		})
	}
}

// claimedImport stages a new book from the file, claims its Job and brings it to the write, as the handler of the
// import Job does; it returns the write of the chapters of the file under that claim.
func (fixture *importWriteFixture) claimedImport(file []byte) library.FinalizeImportStoreInput {
	fixture.t.Helper()
	fixture.imports++
	job, err := library.NewService(fixture.store, library.NewSecureIDGenerator(), auth.SystemClock{}).CreateImportJob(
		fixture.ctx, library.Actor{UserID: fixture.userID}, fixture.projectID, library.CreateImportJobInput{
			SourceFileName: "book.fb2", MIMEType: fb2MIMEType, SizeBytes: int64(len(file)), FileBytes: file,
			IdempotencyKey: "import-refused", RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
			ImportMode: library.ImportModeNewBook, SessionWriteProof: fixture.proof,
		})
	if err != nil {
		fixture.t.Fatal(err)
	}
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != job.ID {
		fixture.t.Fatalf("the claim = %+v, %v", decision.Claim, err)
	}
	chapters := parsedScenarioChapters(fixture.t, bookexport.FormatFB2, file)
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, decision.Claim); err != nil {
		fixture.t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, decision.Claim, len(chapters)); err != nil {
		fixture.t.Fatal(err)
	}
	request, err := fixture.store.GetImportJobForExecution(fixture.ctx, fixture.projectID, job.ID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return library.FinalizeImportStoreInput{
		JobID: job.ID, ProjectID: fixture.projectID, ActorUserID: fixture.userID, StorageKey: request.Metadata.StorageKey,
		ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, BookTitle: "Том 3. Черновик",
		ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
		SourceFileName: "book.fb2", Chapters: chapters,
	}
}
