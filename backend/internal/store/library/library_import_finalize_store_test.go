package librarystore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

func TestLibraryStoreFinalizeImportIsAtomicAndClaimIncarnationBound(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	jobsStore := jobstore.New(db)
	userID := "00000000-0000-4000-8000-000000000840"
	projectID := "00000000-0000-4000-8000-000000000841"
	membershipID := "00000000-0000-4000-8000-000000000842"
	jobID := "00000000-0000-4000-8000-000000000843"
	storageKey := "import-finalize-atomic-test"
	now := time.Now().UTC().Truncate(time.Microsecond)
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, userID, "library-import-finalize@example.com")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Atomic Import Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)

	payload := []byte("<FictionBook>atomic import</FictionBook>")
	payloadSHA256 := sha256.Sum256(payload)
	fileSHA256 := hex.EncodeToString(payloadSHA256[:])
	stagingInput := library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: sessionProof,
		IdempotencyKey: "import-finalize-atomic", RequestMethod: "POST",
		RequestPath: "/projects/" + projectID + "/imports", RequestFingerprint: "finalize-atomic-fingerprint",
		FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: storageKey, SourceFileName: "atomic.fb2", Extension: ".fb2",
			MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), FileSHA256: fileSHA256,
			IdempotencyKey: "import-finalize-atomic", ImportMode: library.ImportModeNewBook,
		},
	}
	if _, err := store.CreateImportStagingForActor(ctx, stagingInput); err != nil {
		t.Fatalf("CreateImportStagingForActor() error = %v", err)
	}
	claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	claim := claimDecision.Claim
	if err != nil {
		t.Fatalf("ClaimNextQueuedJobForWorker() error = %v", err)
	}
	if claim.JobID != jobID || claim.ClaimIncarnation != 1 {
		t.Fatalf("claim = %#v, want job %s incarnation 1", claim, jobID)
	}
	loadedBody, err := store.LoadImportBodyForClaim(ctx, claim, storageKey)
	if err != nil {
		t.Fatalf("LoadImportBodyForClaim() error = %v", err)
	}
	if !bytes.Equal(loadedBody, payload) {
		t.Fatalf("LoadImportBodyForClaim() bytes = %q, want %q", string(loadedBody), string(payload))
	}
	staleClaim := claim
	staleClaim.ClaimIncarnation++
	if _, err := store.LoadImportBodyForClaim(ctx, staleClaim, storageKey); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("LoadImportBodyForClaim(stale claim) error = %v, want ErrNotFound", err)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, storageKey+"-wrong"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("LoadImportBodyForClaim(wrong storage key) error = %v, want ErrNotFound", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET cancel_requested_at = clock_timestamp() WHERE id = $1`, jobID); err != nil {
		t.Fatalf("set import cancellation request: %v", err)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, storageKey); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("LoadImportBodyForClaim(canceled claim) error = %v, want ErrNotFound", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET cancel_requested_at = NULL WHERE id = $1`, jobID); err != nil {
		t.Fatalf("clear import cancellation request: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE import_uploads SET expires_at = clock_timestamp() - interval '1 second' WHERE storage_key = $1`, storageKey); err != nil {
		t.Fatalf("expire import upload: %v", err)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, storageKey); !errors.Is(err, library.ErrImportUploadExpired) {
		t.Fatalf("LoadImportBodyForClaim(expired body) error = %v, want ErrImportUploadExpired", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE import_uploads SET expires_at = clock_timestamp() + interval '24 hours' WHERE storage_key = $1`, storageKey); err != nil {
		t.Fatalf("restore import upload expiry: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE projects SET lifecycle_generation = lifecycle_generation + 1 WHERE id = $1`, projectID); err != nil {
		t.Fatalf("fence import owners: %v", err)
	}
	if _, err := store.LoadImportBodyForClaim(ctx, claim, storageKey); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("LoadImportBodyForClaim(fenced owners) error = %v, want ErrNotFound", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE projects SET lifecycle_generation = lifecycle_generation - 1 WHERE id = $1`, projectID); err != nil {
		t.Fatalf("restore import owner generation: %v", err)
	}

	paragraphText := "First paragraph"
	baseInput := library.FinalizeImportStoreInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: storageKey,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: "Imported Book",
		ImportMode: library.ImportModeNewBook, SourceFileName: "atomic.fb2",
		Chapters: []library.FinalizeImportChapterInput{
			{Title: "Chapter One", Paragraphs: []library.ParagraphInput{{Markdown: paragraphText, Text: &paragraphText}}},
			{Title: "", Paragraphs: []library.ParagraphInput{{Markdown: "must roll back"}}},
		},
	}
	if _, err := store.FinalizeImport(ctx, baseInput); !errors.Is(err, library.ErrValidation) {
		t.Fatalf("FinalizeImport(invalid second chapter) error = %v, want ErrValidation", err)
	}
	assertImportTreeCounts(t, db, projectID, 0, 0, 0)
	current, err := jobsStore.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		t.Fatalf("GetJob(after rollback) error = %v", err)
	}
	if current.Status != jobs.StatusRunning || current.WorkerClaim != claim {
		t.Fatalf("job after rollback = %#v, want same running claim", current)
	}

	baseInput.Chapters[1].Title = "Chapter Two"
	if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
		t.Fatalf("MarkImportParsingForClaim() error = %v", err)
	}
	if err := store.MarkImportSavingForClaim(ctx, claim, len(baseInput.Chapters)); err != nil {
		t.Fatalf("MarkImportSavingForClaim() error = %v", err)
	}
	finalized, err := store.FinalizeImport(ctx, baseInput)
	if err != nil {
		t.Fatalf("FinalizeImport(valid) error = %v", err)
	}
	if finalized.BookID == "" || finalized.ChapterCount != 2 || finalized.ParagraphCount != 2 {
		t.Fatalf("finalized import = %#v", finalized)
	}
	assertImportTreeCounts(t, db, projectID, 1, 2, 2)
	var bookStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM books WHERE project_id = $1 AND id = $2`, projectID, finalized.BookID).Scan(&bookStatus); err != nil {
		t.Fatalf("read imported book status: %v", err)
	}
	if bookStatus != string(library.BookStatusDraft) {
		t.Fatalf("imported book status = %q, want %q until enrichment completes", bookStatus, library.BookStatusDraft)
	}
	var errorJSONIsNull bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT error_json IS NULL
		FROM jobs
		WHERE project_id = $1 AND id = $2
	`, projectID, jobID).Scan(&errorJSONIsNull); err != nil {
		t.Fatalf("read finalized import error state: %v", err)
	}
	if !errorJSONIsNull {
		t.Fatal("successful import retained non-NULL error_json")
	}
	current, err = jobsStore.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		t.Fatalf("GetJob(after success) error = %v", err)
	}
	if current.Status != jobs.StatusSucceeded || current.WorkerClaim != claim || current.ImportProgress == nil ||
		current.ImportProgress.Phase != jobs.ImportPhaseSaved || current.ImportProgress.SavedChapters == nil ||
		*current.ImportProgress.SavedChapters != 2 || current.ImportProgress.SavedAt == nil {
		t.Fatalf("job after finalization = %#v, want succeeded claim 1", current)
	}
	var bodyCleared bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT content IS NULL
		FROM import_uploads
		WHERE project_id = $1 AND storage_key = $2
	`, projectID, storageKey).Scan(&bodyCleared); err != nil {
		t.Fatalf("read finalized import body state: %v", err)
	}
	if !bodyCleared {
		t.Fatal("finalized import retained raw upload bytes")
	}
	if intents := textIndexIntentsForTest(t, db, projectID); len(intents) != 1 {
		t.Fatalf("import text intents = %#v, want one draft intent", intents)
	}
	assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID, []governance.SourceView{governance.SourceViewDraft})
	if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards'`, projectID); got != 0 {
		t.Fatalf("import cards jobs = %d, want none before the text index", got)
	}
	if got := countJobsStoreRowsForTest(t, db, `
		SELECT count(*)
		FROM artifact_source_owners AS owner
		WHERE owner.project_id = $1
		  AND owner.artifact_kind IN ('import_upload', 'import_job_request', 'job')
		  AND owner.ref_count = 3
	`, projectID); got != 3 {
		t.Fatalf("finalized import owners with complete produced refs = %d, want 3", got)
	}
	if _, err := store.FinalizeImport(ctx, baseInput); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("FinalizeImport(stale replay) error = %v, want ErrConflict", err)
	}
	assertImportTreeCounts(t, db, projectID, 1, 2, 2)
}

func TestLibraryStoreFinalizeImportRejectsConcurrentOrderInterleave(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	fixture.input.ExpectedBookOrder = 1
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000009991', $1, 'Concurrent Book', 1, '1', clock_timestamp(), clock_timestamp())
	`, fixture.projectID); err != nil {
		t.Fatalf("insert concurrent book: %v", err)
	}

	_, err := New(fixture.db).FinalizeImport(fixture.ctx, fixture.input)
	if !errors.Is(err, library.ErrImportExpectedOrderConflict) {
		t.Fatalf("FinalizeImport error = %v, want ErrImportExpectedOrderConflict", err)
	}
	if got := countJobsStoreRowsForTest(t, fixture.db, `SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NULL`, fixture.projectID); got != 1 {
		t.Fatalf("active books after rejected finalization = %d, want 1", got)
	}
}

// A book deleted between the staging of an import and its end does not move the place of the new book: it stands after
// the books, where its import was given it (specs/2026-11-11-ux7-upload.md UP2.3).
func TestLibraryStoreFinalizeImportKeepsItsPlaceAfterABookIsDeleted(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	store := New(fixture.db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	bookIDs := []string{
		"00000000-0000-4000-8000-000000009981",
		"00000000-0000-4000-8000-000000009982",
		"00000000-0000-4000-8000-000000009983",
	}
	for index, bookID := range bookIDs {
		order := index + 1
		createBookForOrderTest(t, store, fixture.projectID, bookID, fmt.Sprintf("Existing %d", order), order, fmt.Sprint(order), now)
	}
	fixture.input.ExpectedBookOrder = 4
	if _, err := store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.userID,
		ProjectID:   fixture.projectID,
		BookID:      bookIDs[1],
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
		DeletedAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}

	finalized, err := store.FinalizeImport(fixture.ctx, fixture.input)
	if err != nil {
		t.Fatalf("FinalizeImport() error = %v", err)
	}
	var order int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT "order" FROM books WHERE id = $1`, finalized.BookID).Scan(&order); err != nil {
		t.Fatal(err)
	}
	if order != 4 {
		t.Fatalf("the new book stands at order %d, want 4", order)
	}
}

func TestLibraryStoreFinalizeImportAllowsLegacySubjectWithoutExpectedOrder(t *testing.T) {
	fixture := newImportFinalizeFailpointFixture(t)
	finalized, err := New(fixture.db).FinalizeImport(fixture.ctx, fixture.input)
	if err != nil {
		t.Fatalf("FinalizeImport() error = %v", err)
	}
	if finalized.BookID == "" {
		t.Fatalf("FinalizeImport() = %#v, want created book", finalized)
	}
}

func TestPublishedImportParagraphProjectionPreservesIDsAndRevision(t *testing.T) {
	draft := []library.Paragraph{
		{ID: "11111111-1111-4111-8111-111111111111", Revision: 1, Variant: library.ParagraphVariantDraft},
		{ID: "22222222-2222-4222-8222-222222222222", Revision: 1, Variant: library.ParagraphVariantDraft},
	}
	published := publishedImportParagraphs(draft)
	if len(published) != len(draft) {
		t.Fatalf("published paragraphs = %d, want %d", len(published), len(draft))
	}
	for index := range draft {
		if published[index].ID != draft[index].ID || published[index].Revision != draft[index].Revision ||
			published[index].Variant != library.ParagraphVariantPublished {
			t.Fatalf("published paragraph %d = %#v, draft %#v", index, published[index], draft[index])
		}
		if draft[index].Variant != library.ParagraphVariantDraft {
			t.Fatalf("draft paragraph %d was mutated: %#v", index, draft[index])
		}
	}
}

func TestLibraryStoreFinalizeImportProjectsFrozenPublicationPolicy(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	for _, tt := range []struct {
		name        string
		base        int
		publication library.InitialPublication
		published   bool
	}{
		{name: "draft only", base: 910, publication: library.InitialPublicationDraftOnly},
		{name: "publish imported", base: 920, publication: library.InitialPublicationPublishImported, published: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			userID := fmt.Sprintf("00000000-0000-4000-8000-%012d", tt.base)
			projectID := fmt.Sprintf("00000000-0000-4000-8000-%012d", tt.base+1)
			membershipID := fmt.Sprintf("00000000-0000-4000-8000-%012d", tt.base+2)
			jobID := fmt.Sprintf("00000000-0000-4000-8000-%012d", tt.base+3)
			storageKey := fmt.Sprintf("import-publication-%d", tt.base)
			cleanupLibraryImportStoreRows(t, db, userID, projectID)
			proof := insertProjectStoreUserWithSessionForTest(t, db, userID, fmt.Sprintf("import-publication-%d@example.test", tt.base))
			insertImportStoreProjectForTest(t, db, projectID, userID, "Import Publication", now)
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)
			payload := []byte("<FictionBook>publication projection</FictionBook>")
			digest := sha256.Sum256(payload)
			if _, err := New(db).CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
				JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: proof,
				IdempotencyKey: storageKey + "-key", RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
				RequestFingerprint: storageKey + "-fingerprint", FileBytes: payload,
				Metadata: library.ImportFileMetadata{
					StorageKey: storageKey, SourceFileName: "publication.fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
					SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: storageKey + "-key",
					ImportMode: library.ImportModeNewBook, InitialPublication: tt.publication,
				},
			}); err != nil {
				t.Fatalf("CreateImportStagingForActor error = %v", err)
			}
			var storedPublication string
			if err := db.Pool.QueryRow(ctx, `SELECT initial_publication FROM import_job_requests WHERE project_id = $1 AND job_id = $2`, projectID, jobID).Scan(&storedPublication); err != nil {
				t.Fatalf("read stored initial publication: %v", err)
			}
			if storedPublication != string(tt.publication) {
				t.Fatalf("stored initial publication = %q, want %q", storedPublication, tt.publication)
			}
			loadedRequest, err := New(db).GetImportJobForExecution(ctx, projectID, jobID)
			if err != nil {
				t.Fatalf("GetImportJobForExecution error = %v", err)
			}
			if loadedRequest.Metadata.InitialPublication != tt.publication {
				t.Fatalf("loaded initial publication = %q, want %q", loadedRequest.Metadata.InitialPublication, tt.publication)
			}

			claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
			if err != nil {
				t.Fatalf("ClaimNextQueuedJobForWorker error = %v", err)
			}
			claim := claimDecision.Claim
			if claim.JobID != jobID {
				t.Fatalf("claimed job = %q, want %q", claim.JobID, jobID)
			}
			store := New(db)
			if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
				t.Fatalf("MarkImportParsingForClaim error = %v", err)
			}
			if err := store.MarkImportSavingForClaim(ctx, claim, 2); err != nil {
				t.Fatalf("MarkImportSavingForClaim error = %v", err)
			}
			paragraphOne, paragraphTwo := "First imported paragraph", "Second imported paragraph"
			finalized, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
				JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: storageKey,
				ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: "Imported Publication", ImportMode: library.ImportModeNewBook,
				SourceFileName: "publication.fb2", InitialPublication: tt.publication,
				Chapters: []library.FinalizeImportChapterInput{
					{Title: "Chapter One", Paragraphs: []library.ParagraphInput{{Markdown: paragraphOne, Text: &paragraphOne}}},
					{Title: "Chapter Two", Paragraphs: []library.ParagraphInput{{Markdown: paragraphTwo, Text: &paragraphTwo}}},
				},
			})
			if err != nil {
				t.Fatalf("FinalizeImport error = %v", err)
			}

			var draftBooks, publishedBooks, draftChapters, publishedChapters int
			if err := db.Pool.QueryRow(ctx, `
				SELECT
				  count(*) FILTER (WHERE source_view = 'draft'),
				  count(*) FILTER (WHERE source_view = 'published')
				FROM book_source_members WHERE project_id = $1 AND book_id = $2
			`, projectID, finalized.BookID).Scan(&draftBooks, &publishedBooks); err != nil {
				t.Fatalf("read book source members: %v", err)
			}
			if err := db.Pool.QueryRow(ctx, `
				SELECT
				  count(*) FILTER (WHERE source_view = 'draft'),
				  count(*) FILTER (WHERE source_view = 'published')
				FROM chapter_source_members WHERE project_id = $1 AND book_id = $2
			`, projectID, finalized.BookID).Scan(&draftChapters, &publishedChapters); err != nil {
				t.Fatalf("read chapter source members: %v", err)
			}
			wantPublishedBooks, wantPublishedChapters := 0, 0
			if tt.published {
				wantPublishedBooks, wantPublishedChapters = 1, 2
			}
			if draftBooks != 1 || publishedBooks != wantPublishedBooks || draftChapters != 2 || publishedChapters != wantPublishedChapters {
				t.Fatalf("source members = books %d/%d chapters %d/%d", draftBooks, publishedBooks, draftChapters, publishedChapters)
			}
			var draftRevision, publishedRevision int64
			var publishedMarkers int
			if err := db.Pool.QueryRow(ctx, `
				SELECT
				  max(catalog_revision) FILTER (WHERE source_view = 'draft'),
				  count(*) FILTER (WHERE source_view = 'published'),
				  COALESCE(max(catalog_revision) FILTER (WHERE source_view = 'published'), 0)
				FROM project_source_views WHERE project_id = $1
			`, projectID).Scan(&draftRevision, &publishedMarkers, &publishedRevision); err != nil {
				t.Fatalf("read source view revisions: %v", err)
			}
			wantPublishedMarkers, wantPublishedRevision := 0, int64(0)
			if tt.published {
				wantPublishedMarkers, wantPublishedRevision = 1, 1
			}
			if draftRevision != 2 || publishedMarkers != wantPublishedMarkers || publishedRevision != wantPublishedRevision {
				t.Fatalf("source view revisions = draft %d published %d/%d", draftRevision, publishedMarkers, publishedRevision)
			}
			intentViews := []governance.SourceView{governance.SourceViewDraft}
			if tt.published {
				intentViews = append(intentViews, governance.SourceViewPublished)
			}
			if intents := textIndexIntentsForTest(t, db, projectID); len(intents) != len(intentViews) {
				t.Fatalf("import text intents = %#v, want one per created view", intents)
			}
			assertQueuedTextIntentsMatchCatalogForTest(t, db, projectID, intentViews)
			if got := countJobsStoreRowsForTest(t, db, `SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards'`, projectID); got != 0 {
				t.Fatalf("import cards jobs = %d, want none before the text index", got)
			}

			var draftParagraphs, publishedParagraphs, locatorCount int
			if err := db.Pool.QueryRow(ctx, `
				SELECT
				  count(*) FILTER (WHERE paragraph.variant = 'draft'),
				  count(*) FILTER (WHERE paragraph.variant = 'published'),
				  count(DISTINCT paragraph.id)
				FROM chapter_paragraphs AS paragraph
				JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id
				WHERE chapter.project_id = $1 AND chapter.book_id = $2
			`, projectID, finalized.BookID).Scan(&draftParagraphs, &publishedParagraphs, &locatorCount); err != nil {
				t.Fatalf("read paragraph projections: %v", err)
			}
			wantPublishedParagraphs := 0
			if tt.published {
				wantPublishedParagraphs = 2
			}
			if draftParagraphs != 2 || publishedParagraphs != wantPublishedParagraphs || locatorCount != 2 {
				t.Fatalf("paragraph projections = draft %d published %d locators %d", draftParagraphs, publishedParagraphs, locatorCount)
			}

			var draftStateCount, publishedStateCount int
			if err := db.Pool.QueryRow(ctx, `
				SELECT
				  count(*) FILTER (WHERE status = 'draft' AND published_revision IS NULL AND published_at IS NULL),
				  count(*) FILTER (WHERE status = 'published' AND published_revision = revision AND published_at IS NOT NULL)
				FROM chapters
				WHERE project_id = $1 AND book_id = $2
			`, projectID, finalized.BookID).Scan(&draftStateCount, &publishedStateCount); err != nil {
				t.Fatalf("read chapter publication state: %v", err)
			}
			wantDraftState, wantPublishedState := 2, 0
			if tt.published {
				wantDraftState, wantPublishedState = 0, 2
			}
			if draftStateCount != wantDraftState || publishedStateCount != wantPublishedState {
				t.Fatalf("chapter publication state = draft %d published %d", draftStateCount, publishedStateCount)
			}
		})
	}
}
