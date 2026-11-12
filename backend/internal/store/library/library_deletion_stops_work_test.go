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
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

// A book deletion stops the work on its text (specs/2026-11-08-deletion-stops-processing.md UC1): the running
// cards and thread layer Jobs of its text version turn canceling, the import of the book stops, the import of the
// other book waits, and the cards Job of the version is queued again for the book that is left.
func TestBookDeletionStopsTheWorkOnItsText(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionWorkFixture(t, "d51", 2)
	cards := fixture.claim(t, jobs.KindChapterCards)
	fixture.queueLayer(t)
	layer := fixture.claim(t, jobs.KindSagaLayer)
	firstImport := fixture.queueBookUpdate(t, fixture.bookIDs[0], "first")
	secondImport := fixture.queueBookUpdate(t, fixture.bookIDs[1], "second")

	if _, err := fixture.store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookIDs[0],
		TombstoneID: testUUID(51_001), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}

	assertDeletionJobStatus(t, fixture.db, cards.JobID, jobs.StatusCanceling)
	assertDeletionJobStatus(t, fixture.db, layer.JobID, jobs.StatusCanceling)
	assertDeletionJobStatus(t, fixture.db, firstImport, jobs.StatusCanceled)
	assertDeletionJobStatus(t, fixture.db, secondImport, jobs.StatusQueued)
	if waiting := fixture.waitingCards(t); waiting != 1 {
		t.Fatalf("waiting draft cards Jobs = %d, want the successor for the book that is left", waiting)
	}

	// The worker sees the cancel at its next heartbeat and reads the Job canceling under the same claim.
	claims := jobstore.New(fixture.db)
	if err := claims.HeartbeatWorkerClaim(ctx, cards); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("HeartbeatWorkerClaim() = %v, want the claim not extended", err)
	}
	if current, err := claims.LoadJobForWorkerClaim(ctx, cards); err != nil || current.Status != jobs.StatusCanceling {
		t.Fatalf("LoadJobForWorkerClaim() = %q, %v; want canceling", current.Status, err)
	}
	// A card the running Job writes after the fence is refused, so no card of the deleted book outlives it.
	saga := sagarepo.New(fixture.db)
	if err := saga.SaveCard(ctx, cards, memoryservice.StoredCard{
		CardKey: memoryservice.CardKey{ChapterID: fixture.chapterOf(t, fixture.bookIDs[0]), ContentHash: hashForDeletionTest("late")},
		Model:   "test-model",
	}, 0.5); !errors.Is(err, sagarepo.ErrClaimLost) {
		t.Fatalf("SaveCard() after the fence = %v, want the claim lost", err)
	}
	// Its owner ends it canceled under the claim it had.
	builder := memoryservice.NewBuildCards(saga, saga, deletionCardModel{}, nil, "test-model", 1, nil, nil)
	if err := builder.CancelForClaim(ctx, cards, jobs.ChapterCardsSubject{
		Type: jobs.KindChapterCards, ProjectID: fixture.projectID, SourceView: governance.SourceViewDraft,
	}); err != nil {
		t.Fatalf("CancelForClaim() error = %v", err)
	}
	assertDeletionJobStatus(t, fixture.db, cards.JobID, jobs.StatusCanceled)
}

// A chapter deletion stops the running cards Job of its version and queues it again; the update of the book from a
// file waits, since it pairs the chapters when it ends (R1.2).
func TestChapterDeletionStopsTheCardsOfItsVersion(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionWorkFixture(t, "d52", 2)
	cards := fixture.claim(t, jobs.KindChapterCards)
	update := fixture.queueBookUpdate(t, fixture.bookIDs[0], "chapter")

	if _, err := fixture.store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookIDs[0],
		ChapterID: fixture.chapterOf(t, fixture.bookIDs[0]), TombstoneID: testUUID(52_001), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}

	assertDeletionJobStatus(t, fixture.db, cards.JobID, jobs.StatusCanceling)
	assertDeletionJobStatus(t, fixture.db, update, jobs.StatusQueued)
	if waiting := fixture.waitingCards(t); waiting != 1 {
		t.Fatalf("waiting draft cards Jobs = %d, want the successor", waiting)
	}
}

// The last book of a version leaves nothing to card: the running Job stops and none is queued (R1.3).
func TestDeletingTheLastBookQueuesNoCards(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionWorkFixture(t, "d53", 1)
	cards := fixture.claim(t, jobs.KindChapterCards)

	if _, err := fixture.store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, BookID: fixture.bookIDs[0],
		TombstoneID: testUUID(53_001), DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}

	assertDeletionJobStatus(t, fixture.db, cards.JobID, jobs.StatusCanceling)
	if waiting := fixture.waitingCards(t); waiting != 0 {
		t.Fatalf("waiting draft cards Jobs = %d, want none for an empty version", waiting)
	}
}

type deletionWorkFixture struct {
	db        *storedb.DB
	store     *Store
	claims    *workerClaimAuthorityForLibraryTest
	projectID string
	actorID   string
	proof     auth.SessionWriteProof
	bookIDs   []string
}

// newDeletionWorkFixture imports the books through the import queue, indexes the draft text of each and leaves the
// draft cards Job of the version waiting, as the product does.
func newDeletionWorkFixture(t *testing.T, suffix string, books int) deletionWorkFixture {
	t.Helper()
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, suffix)
	store := New(db)
	orders := make([]int, books)
	for index := range orders {
		orders[index] = index + 1
	}
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "deletion-work-"+suffix, orders...)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	for position, item := range queue.Items {
		decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || decision.Claim.JobID != item.ImportJobID {
			t.Fatalf("import claim %d = %+v, %v; want item %s", position+1, decision.Claim, err, item.ImportJobID)
		}
		if err := store.MarkImportParsingForClaim(ctx, decision.Claim); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkImportSavingForClaim(ctx, decision.Claim, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
			JobID: decision.Claim.JobID, ProjectID: projectID, ActorUserID: actorID,
			ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, ExpectedBookOrder: position + 1,
			StorageKey: item.StorageKey, BookTitle: fmt.Sprintf("Том %d", position+1),
			ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
			SourceFileName: item.SourceFileName,
			Chapters:       []library.FinalizeImportChapterInput{{Title: "Глава", Paragraphs: []library.ParagraphInput{{Markdown: "Текст"}}}},
		}); err != nil {
			t.Fatal(err)
		}
		textClaim, textSubject := claimDraftTextIndexForTest(t, db, projectID)
		indexCurrentTextForTest(t, indexing, textClaim)
		if err := indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Pool.Query(ctx, `SELECT id::text FROM books WHERE project_id = $1 AND deleted_at IS NULL ORDER BY "order"`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	fixture := deletionWorkFixture{db: db, store: store, claims: claims, projectID: projectID, actorID: actorID, proof: proof}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		fixture.bookIDs = append(fixture.bookIDs, id)
	}
	if err := rows.Err(); err != nil || len(fixture.bookIDs) != books {
		t.Fatalf("books = %v, %v; want %d", fixture.bookIDs, err, books)
	}
	return fixture
}

func (fixture deletionWorkFixture) claim(t *testing.T, kind jobs.Kind) jobs.WorkerClaim {
	t.Helper()
	decision, err := fixture.claims.ClaimNextQueuedJobForWorker(context.Background(), []jobs.Kind{kind})
	if err != nil || decision.Claim.ProjectID != fixture.projectID {
		t.Fatalf("%s claim = %+v, %v", kind, decision.Claim, err)
	}
	return decision.Claim
}

func (fixture deletionWorkFixture) queueLayer(t *testing.T) {
	t.Helper()
	if err := indexingstore.New(fixture.db).QueueSagaLayer(context.Background(), fixture.projectID, governance.SourceViewDraft); err != nil {
		t.Fatalf("QueueSagaLayer() error = %v", err)
	}
}

// queueBookUpdate queues «Обновить из файла» of a book and returns its import Job.
func (fixture deletionWorkFixture) queueBookUpdate(t *testing.T, bookID, key string) string {
	t.Helper()
	payload := []byte("book update " + key)
	digest := sha256.Sum256(payload)
	jobID := testUUID(50_000 + int(digest[0])*256 + int(digest[1]))
	key = "deletion-update-" + key + "-" + fixture.projectID
	if _, err := fixture.store.CreateImportStagingForActor(context.Background(), library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.actorID, SessionWriteProof: fixture.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: "tom.fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: library.ImportModeUpdateBook, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
	}
	return jobID
}

func (fixture deletionWorkFixture) chapterOf(t *testing.T, bookID string) string {
	t.Helper()
	var id string
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT id::text FROM chapters WHERE project_id = $1 AND book_id = $2 ORDER BY "order" LIMIT 1
	`, fixture.projectID, bookID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (fixture deletionWorkFixture) waitingCards(t *testing.T) int {
	t.Helper()
	var waiting int
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM jobs
		WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND subject_json->>'sourceView' = 'draft'
	`, fixture.projectID).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	return waiting
}

func assertDeletionJobStatus(t *testing.T, db *storedb.DB, jobID string, want jobs.Status) {
	t.Helper()
	var status string
	var cancelRequested bool
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT status, cancel_requested_at IS NOT NULL FROM jobs WHERE id = $1
	`, jobID).Scan(&status, &cancelRequested); err != nil {
		t.Fatal(err)
	}
	stopped := want == jobs.StatusCanceling || want == jobs.StatusCanceled
	if status != string(want) || cancelRequested != stopped {
		t.Fatalf("Job %s = %s (cancel requested %v), want %s", jobID, status, cancelRequested, want)
	}
}

func hashForDeletionTest(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

type deletionCardModel struct{}

func (deletionCardModel) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	return "", quota.ModelUsage{}, errors.New("the deletion test calls no model")
}
