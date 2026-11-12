package librarystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// markDemoSagaForTest gives the project of the fixture the sign of the demo saga. The store of projects sets it at
// creation (projectstore, TestAnOwnerHasOneLiveDemoSaga); this package cannot import that store, so its fixture
// project, a row of the test helpers, takes the sign the same way.
func (fixture *quotaImportFixture) markDemoSagaForTest() {
	fixture.t.Helper()
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE projects SET demo = true WHERE id = $1`, fixture.projectID); err != nil {
		fixture.t.Fatal(err)
	}
}

// stage stages one import under its own key and returns the id of its Job with the error of the staging.
func (fixture *quotaImportFixture) stage(key string, seed bool, mode library.ImportMode, bookID string) (string, error) {
	fixture.t.Helper()
	fixture.jobs++
	jobID := fmt.Sprintf("00000000-0000-4000-8000-000000000c%02d", fixture.jobs)
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	job, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.actorID, SessionWriteProof: fixture.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload, Seed: seed,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	})
	return job.ID, err
}

// finalize claims the staged import and writes its book of one chapter with text of the given length.
func (fixture *quotaImportFixture) finalize(jobID, key string, length int) (library.FinalizedImport, error) {
	fixture.t.Helper()
	decision, err := newWorkerClaimAuthorityForTest(fixture.db).ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != jobID {
		fixture.t.Fatalf("claim %s = %+v, %v", key, decision.Claim, err)
	}
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, decision.Claim); err != nil {
		fixture.t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, decision.Claim, 1); err != nil {
		fixture.t.Fatal(err)
	}
	text := strings.Repeat("я", length)
	return fixture.store.FinalizeImport(fixture.ctx, library.FinalizeImportStoreInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.actorID, StorageKey: key,
		ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, BookTitle: "Seed " + key,
		ImportMode: library.ImportModeNewBook, SourceFileName: key + ".fb2",
		InitialPublication: library.InitialPublicationPublishImported,
		Chapters: []library.FinalizeImportChapterInput{{
			Title: "Chapter", Paragraphs: []library.ParagraphInput{{Markdown: text, Text: &text}},
		}},
	})
}

// specs/2026-11-09-demo-saga.md DS3.3, DS3.4, DS1.4: the demo saga takes one text — its seed — and nothing else. The
// seed goes into no saga of the author's own; it passes with the owner at every limit of his text and of his day and
// spends no upload; a second seed next to a going import or a live book is a conflict. Every write of the author that
// adds or changes text — an import in any mode, a new book, a queue, a new chapter, an edit of a chapter — is refused
// with the refusal of the demo saga, not with a limit. Its book is deleted and restored as any book, and the saga
// without its book takes the seed again.
func TestTheDemoSagaTakesNoTextButItsSeed(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	for _, key := range []quota.Key{
		quota.KeyProjectBooks, quota.KeyAccountBooks, quota.KeyProjectChapters, quota.KeyAccountChapters,
		quota.KeyProjectCharacters, quota.KeyAccountCharacters, quota.KeyImportsPerDay,
	} {
		fixture.limit(fixture.ownerID, key, 0)
	}
	strangerBook := "00000000-0000-4000-8000-000000000cf0"

	// The seed is the text of the demo saga alone.
	if _, err := fixture.stage("seed-into-his-own", true, library.ImportModeNewBook, ""); !errors.Is(err, library.ErrValidation) {
		t.Fatalf("a seed into a saga of his own = %v; want library.ErrValidation", err)
	}
	fixture.markDemoSagaForTest()

	// No import of the author goes into the demo saga, whatever its mode.
	for name, attempt := range map[string]struct {
		mode   library.ImportMode
		bookID string
	}{
		"a new book":             {library.ImportModeNewBook, ""},
		"an update of a book":    {library.ImportModeUpdateBook, strangerBook},
		"a preview of an update": {library.ImportModeUpdatePreview, strangerBook},
	} {
		if _, err := fixture.stage("author-"+strings.ReplaceAll(name, " ", "-"), false, attempt.mode, attempt.bookID); !errors.Is(err, quota.ErrDemoSaga) {
			t.Fatalf("%s into the demo saga = %v; want quota.ErrDemoSaga", name, err)
		}
	}
	if _, err := fixture.stage("seed-over-a-book", true, library.ImportModeUpdateBook, strangerBook); !errors.Is(err, library.ErrValidation) {
		t.Fatalf("a seed that updates a book = %v; want library.ErrValidation", err)
	}

	// The seed passes with the owner at every limit, and it is no upload of his day.
	seedJob, err := fixture.stage("seed", true, library.ImportModeNewBook, "")
	if err != nil {
		t.Fatalf("the seed with the owner at every limit: %v", err)
	}
	if uploads := fixture.count(`SELECT COALESCE(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, fixture.ownerID); uploads != 0 {
		t.Fatalf("the seed spent %d uploads of the day; want none", uploads)
	}
	// The same request again is the same Job; another seed next to the going one is a conflict.
	if replayed, err := fixture.stage("seed", true, library.ImportModeNewBook, ""); err != nil || replayed != seedJob {
		t.Fatalf("the seed replayed = %s, %v; want the Job %s", replayed, err, seedJob)
	}
	if _, err := fixture.stage("seed-next-to-a-going-one", true, library.ImportModeNewBook, ""); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("a second seed next to a going import = %v; want library.ErrConflict", err)
	}
	seeded, err := fixture.finalize(seedJob, "seed", 1200)
	if err != nil {
		t.Fatalf("the seed written with the owner at every limit: %v", err)
	}
	// The seed is not a book its author waits for: it opens no wave of «Сага готова» — no letter, no promise of one, no
	// notice of the site (specs/2026-11-09-first-session.md FS1.2).
	if waves := fixture.count(`SELECT count(*) FROM saga_ready_waves WHERE project_id = $1`, fixture.projectID); waves != 0 {
		t.Fatalf("the seed of the demo saga opened %d waves of «Сага готова»; want none", waves)
	}
	if _, err := fixture.stage("seed-next-to-the-book", true, library.ImportModeNewBook, ""); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("a second seed next to the live book = %v; want library.ErrConflict", err)
	}
	if jobs := fixture.count(`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'import'`, fixture.projectID); jobs != 1 {
		t.Fatalf("imports of the demo saga = %d; want the seed alone", jobs)
	}

	// The text of the demo saga is not a text of its owner.
	account, err := quotastore.New(fixture.db, quotastore.Fence{}).AccountQuota(fixture.ctx, fixture.ownerID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources != (quota.Sources{}) || account.Projects != 0 {
		t.Fatalf("the account of the owner = %d sagas, %+v; want nothing of the demo saga", account.Projects, account.Sources)
	}

	// Nothing of the author is added to the seeded saga, and nothing in it is changed.
	if _, err := fixture.stage("author-update-of-the-seed", false, library.ImportModeUpdateBook, seeded.BookID); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("an update of the book of the demo saga = %v; want quota.ErrDemoSaga", err)
	}
	if _, err := fixture.store.CreateBook(fixture.ctx, library.CreateBookStoreInput{
		ID: "00000000-0000-4000-8000-000000000cf1", ProjectID: fixture.projectID, Title: "Своя книга",
		Order: 2, DisplayNumber: "2", CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("a new book in the demo saga = %v; want quota.ErrDemoSaga", err)
	}
	if _, err := fixture.store.CreateImportQueueDraftForActor(fixture.ctx, library.CreateImportQueueDraftStoreInput{
		ProjectID: fixture.projectID, ActorUserID: fixture.actorID, IdempotencyKey: "queue-into-the-demo-saga",
		RequestFingerprint: "queue-into-the-demo-saga-fingerprint",
		Items: []library.CreateImportQueueItemStoreInput{{
			ClientItemID: "item-a", Position: 1, SourceFileName: "book-2.fb2",
		}},
	}); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("an import queue into the demo saga = %v; want quota.ErrDemoSaga", err)
	}
	var chapterID string
	var revision int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT id::text, revision FROM chapters WHERE book_id = $1 AND deleted_at IS NULL ORDER BY "order" LIMIT 1
	`, seeded.BookID).Scan(&chapterID, &revision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		_, err := fixture.store.ReplaceChapterTextInTx(fixture.ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: fixture.projectID, BookID: seeded.BookID, ChapterID: chapterID, ExpectedRevision: revision,
			Title: "Chapter", Paragraphs: []string{"Правка главы примера."},
		})
		return err
	}); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("an edit of a chapter of the demo saga = %v; want quota.ErrDemoSaga", err)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		_, err := fixture.store.AddChapterInTx(fixture.ctx, tx, library.AddChapterInput{
			ProjectID: fixture.projectID, BookID: seeded.BookID, AfterChapterID: chapterID, Title: "Своя глава",
			Paragraphs: []string{"Глава автора в примере."}, At: time.Now(),
		})
		return err
	}); !errors.Is(err, quota.ErrDemoSaga) {
		t.Fatalf("a new chapter in the demo saga = %v; want quota.ErrDemoSaga", err)
	}
	if characters := fixture.count(`
		SELECT COALESCE(sum(length(paragraph.plain_text)), 0)
		FROM chapters AS chapter
		JOIN chapter_paragraphs AS paragraph
		  ON paragraph.chapter_id = chapter.id AND paragraph.revision = chapter.revision AND paragraph.variant = 'draft'
		WHERE chapter.book_id = $1 AND chapter.deleted_at IS NULL
	`, seeded.BookID); characters != 1200 {
		t.Fatalf("the text of the demo saga after the refusals = %d characters; want the 1200 of its seed", characters)
	}

	// Its book goes to the trash and back as any book, over every limit of the owner; without its book the saga
	// takes the seed again.
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: seeded.BookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	if _, err := fixture.store.RestoreBook(fixture.ctx, library.RestoreBookStoreInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: seeded.BookID,
	}); err != nil {
		t.Fatalf("the book of the demo saga restored with the owner at every limit: %v", err)
	}
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: seeded.BookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	if _, err := fixture.stage("seed-again", true, library.ImportModeNewBook, ""); err != nil {
		t.Fatalf("the seed of the demo saga without its book: %v", err)
	}
}
