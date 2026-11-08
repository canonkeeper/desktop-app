package indexingstore

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

var _ indexing.Store = (*Store)(nil)

func TestStoreStartIndexingCreatesReusesAndReplaysExactViewJob(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1400)

	first := startIndexingInputForStoreTest(
		fixture, indexingStoreUUIDForTest(1901), indexingStoreUUIDForTest(1902), "key-a", "request-a",
	)
	created, err := store.StartIndexing(ctx, first)
	if err != nil {
		t.Fatalf("first StartIndexing() error = %v", err)
	}

	reuse := startIndexingInputForStoreTest(
		fixture, indexingStoreUUIDForTest(1903), indexingStoreUUIDForTest(1904), "key-b", "request-b",
	)
	reused, err := store.StartIndexing(ctx, reuse)
	if err != nil {
		t.Fatalf("reuse StartIndexing() error = %v", err)
	}
	replay := startIndexingInputForStoreTest(
		fixture, indexingStoreUUIDForTest(1905), indexingStoreUUIDForTest(1906), "key-b", "request-b",
	)
	replayed, err := store.StartIndexing(ctx, replay)
	if err != nil {
		t.Fatalf("replay StartIndexing() error = %v", err)
	}

	if created.ID != first.CandidateJobID || reused.ID != created.ID || replayed.ID != created.ID {
		t.Fatalf("created/reused/replayed IDs = %q/%q/%q, want %q", created.ID, reused.ID, replayed.ID, first.CandidateJobID)
	}
	if created.Kind != jobs.KindCanonTextIndex {
		t.Fatalf("job kind = %q, want %q", created.Kind, jobs.KindCanonTextIndex)
	}
	subject, ok := created.Subject.(jobs.CanonTextIndexSubject)
	if !ok || subject.ProjectID != fixture.projectID || subject.SourceView != governance.SourceViewDraft || subject.CatalogRevision != 1 {
		t.Fatalf("job subject = %#v, want current draft revision", created.Subject)
	}

	conflict := replay
	conflict.RequestHash = "different-request"
	if _, err := store.StartIndexing(ctx, conflict); !errors.Is(err, indexing.ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrConflict", err)
	}
	wrongView := replay
	wrongView.SourceView = governance.SourceViewPublished
	if _, err := store.StartIndexing(ctx, wrongView); !errors.Is(err, indexing.ErrConflict) {
		t.Fatalf("cross-view replay error = %v, want ErrConflict", err)
	}

	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='canon_text_index'", fixture.projectID); got != 1 {
		t.Fatalf("text-index jobs = %d, want 1", got)
	}
	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM idempotency_records WHERE operation_id=$1", first.OperationID); got != 2 {
		t.Fatalf("idempotency records = %d, want 2", got)
	}
	assertTextIndexJobProjectRef(t, db, fixture, created.ID)
}

func TestStoreStartIndexingRejectsInvalidSessionWithoutWrites(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	store := New(db)
	fixture := createIndexingStoreFixtureForTest(t, db, 1500)
	input := startIndexingInputForStoreTest(
		fixture, indexingStoreUUIDForTest(1911), indexingStoreUUIDForTest(1912), "key", "request",
	)
	input.SessionWriteProof = auth.SessionWriteProof{}

	_, err := store.StartIndexing(context.Background(), input)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("StartIndexing() error = %v, want ErrUnauthenticated", err)
	}
	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM jobs WHERE project_id=$1", fixture.projectID); got != 0 {
		t.Fatalf("jobs after rejected session = %d, want 0", got)
	}
	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM idempotency_records WHERE operation_id=$1", input.OperationID); got != 0 {
		t.Fatalf("idempotency records after rejected session = %d, want 0", got)
	}
}

func startIndexingInputForStoreTest(
	fixture indexingStoreFixtureForTest,
	jobID, recordID, keyHash, requestHash string,
) indexing.StartIndexingStoreInput {
	return indexing.StartIndexingStoreInput{
		CandidateJobID: jobID, IdempotencyRecordID: recordID,
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID,
		SourceView: governance.SourceViewDraft,
		ScopeHash:  "scope-hash", OperationID: "canon.text.index.start.v1",
		IdempotencyKeyHash: keyHash, RequestHash: requestHash,
		IdempotencyStatusCode: 202, CreatedAt: indexingStoreTestNow(),
		SessionWriteProof: fixture.sessionProof,
	}
}

func assertTextIndexJobProjectRef(t *testing.T, db *storedb.DB, fixture indexingStoreFixtureForTest, jobID string) {
	t.Helper()
	var artifactKind, artifactID, ownerView, scopeKind, refView, payloadState string
	var projectGeneration int64
	var bookID, chapterID pgtype.UUID
	var sourceIndependent bool
	err := db.Pool.QueryRow(context.Background(), `
		SELECT owner.artifact_kind, owner.artifact_id::text, owner.source_view,
		       ref.scope_kind, ref.source_view, ref.project_generation,
		       ref.book_id, ref.chapter_id, ref.source_independent,
		       job.governance_payload_state
		FROM jobs AS job
		JOIN artifact_source_owners AS owner
		  ON owner.id=job.artifact_source_owner_id AND owner.project_id=job.project_id
		JOIN artifact_source_refs AS ref
		  ON ref.owner_id=owner.id AND ref.project_id=owner.project_id
		WHERE job.id=$1 AND job.project_id=$2
	`, jobID, fixture.projectID).Scan(
		&artifactKind, &artifactID, &ownerView, &scopeKind, &refView, &projectGeneration,
		&bookID, &chapterID, &sourceIndependent, &payloadState,
	)
	if err != nil {
		t.Fatalf("load text-index source ref: %v", err)
	}
	if artifactKind != "job" || artifactID != jobID || ownerView != "draft" ||
		scopeKind != "project" || refView != "draft" || projectGeneration != 1 ||
		bookID.Valid || chapterID.Valid || sourceIndependent || payloadState != "active" {
		t.Fatalf("source ref = kind %q id %q owner %q scope %q view %q generation %d book %v chapter %v independent %v payload %q",
			artifactKind, artifactID, ownerView, scopeKind, refView, projectGeneration,
			bookID.Valid, chapterID.Valid, sourceIndependent, payloadState)
	}
}

func countStoreRowsForTest(t *testing.T, db *storedb.DB, query, arg string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), query, arg).Scan(&count); err != nil {
		t.Fatalf("count store rows: %v", err)
	}
	return count
}

// R1.4.2, R1.4.3: an unverified actor starts no reindexing and writes nothing.
func TestStoreStartIndexingRequiresVerifiedEmailWithoutWrites(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	fixture := createIndexingStoreFixtureForTest(t, db, 1600)
	if _, err := db.Pool.Exec(context.Background(), "UPDATE users SET email_verified_at = NULL WHERE id = $1", fixture.actorID); err != nil {
		t.Fatal(err)
	}
	input := startIndexingInputForStoreTest(
		fixture, indexingStoreUUIDForTest(1921), indexingStoreUUIDForTest(1922), "key", "request",
	)
	if _, err := New(db).StartIndexing(context.Background(), input); !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("StartIndexing() error = %v, want ErrEmailNotVerified", err)
	}
	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM jobs WHERE project_id=$1", fixture.projectID); got != 0 {
		t.Fatalf("jobs after unverified start = %d, want 0", got)
	}
	if got := countStoreRowsForTest(t, db, "SELECT count(*) FROM idempotency_records WHERE operation_id=$1", input.OperationID); got != 0 {
		t.Fatalf("idempotency records after unverified start = %d, want 0", got)
	}
}
