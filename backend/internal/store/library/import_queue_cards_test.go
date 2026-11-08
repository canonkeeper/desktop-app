package librarystore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
)

// R2.4: the draft text index makes each imported volume ready and queues the cards of the
// version; the Import Queue takes the next volume and completes without waiting for cards.
func TestDraftTextIndexReleasesImportQueueWithoutCards(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "c41")
	store := New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "queue-cards-c41", 1, 2)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)

	for position, item := range queue.Items {
		decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || decision.Claim.JobID != item.ImportJobID {
			t.Fatalf("import claim %d = %+v, %v; want item %s", position+1, decision.Claim, err, item.ImportJobID)
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
			ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: position + 1,
			StorageKey: item.StorageKey, BookTitle: fmt.Sprintf("Том %d", position+1),
			ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
			SourceFileName: item.SourceFileName,
			Chapters:       []library.FinalizeImportChapterInput{{Title: "Глава", Paragraphs: []library.ParagraphInput{{Markdown: "Текст"}}}},
		}); err != nil {
			t.Fatal(err)
		}
		if position == 0 {
			if _, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport}); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("next volume claimed before its predecessor is ready: %v", err)
			}
		}
		textClaim, textSubject := claimDraftTextIndexForTest(t, db, projectID)
		indexCurrentTextForTest(t, indexing, textClaim)
		if err := indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
			t.Fatal(err)
		}
	}

	var readyBooks, draftCards, enrichment int
	var queueStatus string
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM books WHERE project_id=$1 AND status='ready' AND deleted_at IS NULL),
		  (SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards' AND status='queued'
		     AND subject_json->>'sourceView'='draft'),
		  (SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='canon_book_enrichment'),
		  (SELECT status FROM import_queues WHERE project_id=$1 AND id=$2)
	`, projectID, queue.ID).Scan(&readyBooks, &draftCards, &enrichment, &queueStatus); err != nil {
		t.Fatal(err)
	}
	if readyBooks != 2 || draftCards != 1 || enrichment != 0 || queueStatus != string(library.ImportQueueSucceeded) {
		t.Fatalf("ready books/draft cards Jobs/enrichment/queue = %d/%d/%d/%s, want 2/1/0/succeeded",
			readyBooks, draftCards, enrichment, queueStatus)
	}
}

func claimDraftTextIndexForTest(t *testing.T, db *storedb.DB, projectID string) (jobs.WorkerClaim, jobs.CanonTextIndexSubject) {
	t.Helper()
	var jobID string
	var revision uint64
	if err := db.Pool.QueryRow(context.Background(), `
		UPDATE jobs SET status='running', claim_incarnation=claim_incarnation+1, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
		WHERE project_id=$1 AND kind='canon_text_index' AND status='queued' AND subject_json->>'sourceView'='draft'
		RETURNING id::text, (subject_json->>'catalogRevision')::bigint
	`, projectID).Scan(&jobID, &revision); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: 1}, jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: projectID, SourceView: governance.SourceViewDraft, CatalogRevision: revision,
	}
}

func indexCurrentTextForTest(t *testing.T, indexing *indexingstore.Store, claim jobs.WorkerClaim) {
	t.Helper()
	ctx := context.Background()
	descriptors, err := indexing.ListCanonTextChapterDescriptorsForClaim(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		snapshot, err := indexing.LoadCanonTextChapterSnapshotForClaim(ctx, claim, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := indexing.ReplaceCanonTextChapterForClaim(ctx, indexingstore.ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
}
