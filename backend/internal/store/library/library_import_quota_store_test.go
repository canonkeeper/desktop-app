package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

type quotaImportFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	ownerID   string
	actorID   string
	projectID string
	proof     auth.SessionWriteProof
	jobs      int
}

// newQuotaImportFixture: the project belongs to ownerID; actorID imports into it as an editor
// (the owner himself when actorID == ownerID).
func newQuotaImportFixture(t *testing.T, separateActor bool) *quotaImportFixture {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := &quotaImportFixture{
		t: t, ctx: context.Background(), db: db, store: New(db),
		ownerID: "00000000-0000-4000-8000-000000000a70", projectID: "00000000-0000-4000-8000-000000000a71",
	}
	fixture.proof = insertProjectStoreUserWithSessionForTest(t, db, fixture.ownerID, "quota-owner@example.test")
	fixture.actorID = fixture.ownerID
	insertImportStoreProjectForTest(t, db, fixture.projectID, fixture.ownerID, "Quota Import", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000a72", fixture.projectID, fixture.ownerID, projects.RoleOwner, now, false)
	if separateActor {
		fixture.actorID = "00000000-0000-4000-8000-000000000a73"
		fixture.proof = insertProjectStoreUserWithSessionForTest(t, db, fixture.actorID, "quota-editor@example.test")
		insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000a74", fixture.projectID, fixture.actorID, projects.RoleEditor, now, false)
	}
	return fixture
}

func (fixture *quotaImportFixture) limit(userID string, key quota.Key, value int64) {
	fixture.t.Helper()
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(fixture.ctx, tx, userID, []quota.OverrideChange{{Key: key, Value: &value}}, time.Now())
		return err
	}); err != nil {
		fixture.t.Fatal(err)
	}
}

// importText stages, claims and finalizes one import whose single chapter holds text of the
// given length; it returns the FinalizeImport error.
func (fixture *quotaImportFixture) importText(mode library.ImportMode, bookID string, length int) (library.FinalizedImport, error) {
	fixture.t.Helper()
	fixture.jobs++
	key := "quota-import-" + string(rune('a'+fixture.jobs))
	jobID := "00000000-0000-4000-8000-000000000b7" + string(rune('0'+fixture.jobs))
	payload := []byte("<FictionBook>" + key + "</FictionBook>")
	digest := sha256.Sum256(payload)
	if _, err := fixture.store.CreateImportStagingForActor(fixture.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: fixture.projectID, ActorUserID: fixture.actorID, SessionWriteProof: fixture.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + fixture.projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
			ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		fixture.t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
	}
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
		ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, BookTitle: "Quota " + key, BookID: bookID,
		ImportMode: mode, SourceFileName: key + ".fb2", InitialPublication: library.InitialPublicationPublishImported,
		Chapters: []library.FinalizeImportChapterInput{{
			Title: "Chapter", Paragraphs: []library.ParagraphInput{{Markdown: text, Text: &text}},
		}},
	})
}

func (fixture *quotaImportFixture) count(query string, args ...any) int64 {
	fixture.t.Helper()
	var value int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, query, args...).Scan(&value); err != nil {
		fixture.t.Fatal(err)
	}
	return value
}

func wantQuotaKey(t *testing.T, err error, key quota.Key) {
	t.Helper()
	if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != key {
		t.Fatalf("error = %v, want quota %s", err, key)
	}
}

// R3.3, R3.5: a book that takes the project text over the limit is not written; one within it is.
func TestFinalizeImportRefusesBookOverProjectCharacters(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	fixture.limit(fixture.ownerID, quota.KeyProjectCharacters, 1000)
	_, err := fixture.importText(library.ImportModeNewBook, "", 1200)
	wantQuotaKey(t, err, quota.KeyProjectCharacters)
	if books := fixture.count(`SELECT count(*) FROM books WHERE project_id = $1`, fixture.projectID); books != 0 {
		t.Fatalf("refused import left %d books", books)
	}
	if _, err := fixture.importText(library.ImportModeNewBook, "", 800); err != nil {
		t.Fatalf("import within the limit: %v", err)
	}
}

// R3.3: an update that takes the text over the limit rolls back; the book keeps its text.
func TestFinalizeImportUpdateOverQuotaKeepsBook(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	first, err := fixture.importText(library.ImportModeNewBook, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	fixture.limit(fixture.ownerID, quota.KeyProjectCharacters, 1000)
	_, err = fixture.importText(library.ImportModeUpdateBook, first.BookID, 1200)
	wantQuotaKey(t, err, quota.KeyProjectCharacters)
	if characters := fixture.count(`
		SELECT COALESCE(sum(length(paragraph.plain_text)), 0)
		FROM chapters AS chapter
		JOIN chapter_paragraphs AS paragraph
		  ON paragraph.chapter_id = chapter.id AND paragraph.revision = chapter.revision AND paragraph.variant = 'draft'
		WHERE chapter.book_id = $1 AND chapter.deleted_at IS NULL
	`, first.BookID); characters != 500 {
		t.Fatalf("book text after refused update = %d characters, want 500", characters)
	}
}

// R3.4, R3.5: a book in «Недавно удалённое» does not count; restoring it over a full limit is
// refused and the book stays deleted.
func TestRestoreBookRefusedOverQuota(t *testing.T) {
	fixture := newQuotaImportFixture(t, false)
	first, err := fixture.importText(library.ImportModeNewBook, "", 600)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.importText(library.ImportModeNewBook, "", 300)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: second.BookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceBookDeletion() error = %v", err)
	}
	fixture.limit(fixture.ownerID, quota.KeyProjectCharacters, 800)
	if _, err := fixture.importText(library.ImportModeUpdateBook, first.BookID, 700); err != nil {
		t.Fatalf("a deleted book counted against the limit: %v", err)
	}
	_, err = fixture.store.RestoreBook(fixture.ctx, library.RestoreBookStoreInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: second.BookID,
	})
	wantQuotaKey(t, err, quota.KeyProjectCharacters)
	if deleted := fixture.count(`SELECT count(*) FROM books WHERE id = $1 AND deleted_at IS NOT NULL`, second.BookID); deleted != 1 {
		t.Fatal("refused restore brought the book back")
	}
}

// R1.4: an editor's upload counts for the editor; the book and its text count against the
// project owner.
func TestImportCountsUploadForActorAndTextForOwner(t *testing.T) {
	fixture := newQuotaImportFixture(t, true)
	fixture.limit(fixture.ownerID, quota.KeyProjectCharacters, 1000)
	_, err := fixture.importText(library.ImportModeNewBook, "", 1200)
	wantQuotaKey(t, err, quota.KeyProjectCharacters)
	if imports := fixture.count(`SELECT COALESCE(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, fixture.actorID); imports != 1 {
		t.Fatalf("editor uploads = %d, want 1", imports)
	}
	if rows := fixture.count(`SELECT count(*) FROM writer_usage_days WHERE user_id = $1`, fixture.ownerID); rows != 0 {
		t.Fatalf("owner usage rows = %d, want 0", rows)
	}
}

// R3.1: a queue of 5 volumes with 3 uploads left today is refused and stays a draft.
func TestStartImportQueueRefusedOverDailyUploads(t *testing.T) {
	db, projectID, actorID, proof := newImportQueueStartFixture(t, "a77")
	store := New(db)
	value := int64(3)
	if err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(context.Background(), tx, actorID,
			[]quota.OverrideChange{{Key: quota.KeyImportsPerDay, Value: &value}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	queue := readyImportQueue(t, store, projectID, actorID, "queue-quota", 1, 2, 3, 4, 5)
	_, err := store.StartImportQueueForActor(context.Background(), library.StartImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID, ExpectedVersion: queue.Version,
		IdempotencyKey: "queue-quota", RequestMethod: "POST",
		RequestPath:        "/projects/" + projectID + "/import-queues/" + queue.ID + "/start",
		RequestFingerprint: "queue-quota-fingerprint", SessionWriteProof: proof,
	})
	wantQuotaKey(t, err, quota.KeyImportsPerDay)
	current, err := store.GetImportQueueForActor(context.Background(), library.GetImportQueueStoreInput{
		ProjectID: projectID, QueueID: queue.ID, ActorUserID: actorID,
	})
	if err != nil || current.Status != library.ImportQueueDraft {
		t.Fatalf("queue after refusal = %+v, %v; want a draft", current.Status, err)
	}
	if errors.Is(err, library.ErrConflict) {
		t.Fatal("unexpected conflict")
	}
}
