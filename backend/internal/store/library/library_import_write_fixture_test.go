package librarystore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// fb2MIMEType is the type a book in FictionBook is imported under.
const fb2MIMEType = "application/x-fictionbook+xml"

type importWriteFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	userID    string
	projectID string
	proof     auth.SessionWriteProof
	imports   int
}

// newImportWriteFixture is a saga of its owner with room for the books the tests write: its own limits of text are
// raised, so the write is measured and dumped, not refused.
func newImportWriteFixture(t *testing.T, db *storedb.DB) *importWriteFixture {
	t.Helper()
	ctx := context.Background()
	fixture := &importWriteFixture{
		t: t, ctx: ctx, db: db, store: New(db),
		userID: "00000000-0000-4000-8000-000000000e60", projectID: "00000000-0000-4000-8000-000000000e61",
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture.proof = insertProjectStoreUserWithSessionForTest(t, db, fixture.userID, "import-write@example.test")
	insertImportStoreProjectForTest(t, db, fixture.projectID, fixture.userID, "Сага автора", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000e62", fixture.projectID, fixture.userID, projects.RoleOwner, now, false)
	large := int64(100_000_000)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(ctx, tx, fixture.userID, []quota.OverrideChange{
			{Key: quota.KeyProjectCharacters, Value: &large}, {Key: quota.KeyAccountCharacters, Value: &large},
			{Key: quota.KeyProjectChapters, Value: &large}, {Key: quota.KeyAccountChapters, Value: &large},
			{Key: quota.KeyImportsPerDay, Value: &large},
		}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// importFile imports a file the way the product does: the use case stages it, a worker claims its Job and the
// handler of the import Job reads and writes it.
func (fixture *importWriteFixture) importFile(mode library.ImportMode, bookID, name string, file []byte) library.FinalizedImport {
	fixture.t.Helper()
	return fixture.importFileThrough(fixture.store, mode, bookID, name, file)
}

// importFileThrough imports a file with the given store of the import Job under the handler.
func (fixture *importWriteFixture) importFileThrough(service library.ImportLibraryService, mode library.ImportMode, bookID, name string, file []byte) library.FinalizedImport {
	fixture.t.Helper()
	fixture.imports++
	job, err := library.NewService(fixture.store, library.NewSecureIDGenerator(), auth.SystemClock{}).CreateImportJob(
		fixture.ctx, library.Actor{UserID: fixture.userID}, fixture.projectID, library.CreateImportJobInput{
			SourceFileName: name, MIMEType: fb2MIMEType, SizeBytes: int64(len(file)), FileBytes: file,
			IdempotencyKey: fmt.Sprintf("import-write-%d", fixture.imports), RequestMethod: "POST",
			RequestPath: "/projects/" + fixture.projectID + "/imports", ImportMode: mode, BookID: bookID,
			SessionWriteProof: fixture.proof,
		})
	if err != nil {
		fixture.t.Fatalf("the import of %s: %v", name, err)
	}
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != job.ID {
		fixture.t.Fatalf("the claim of %s = %+v, %v", name, decision.Claim, err)
	}
	claimed, err := jobstore.New(fixture.db).LoadJobForWorkerClaim(fixture.ctx, decision.Claim)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if result, err := library.NewImportHandler(fixture.store, service).HandleResult(fixture.ctx, claimed); err != nil || result.Status != jobs.StatusSucceeded {
		fixture.t.Fatalf("the import Job of %s = %+v, %v; want it succeeded", name, result, err)
	}
	var resultJSON []byte
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT result_json FROM jobs WHERE id = $1`, job.ID).Scan(&resultJSON); err != nil {
		fixture.t.Fatal(err)
	}
	var result jobs.ImportJobResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		fixture.t.Fatal(err)
	}
	return library.FinalizedImport{BookID: result.BookID, ChapterCount: result.ChapterCount, ParagraphCount: result.ParagraphCount}
}
