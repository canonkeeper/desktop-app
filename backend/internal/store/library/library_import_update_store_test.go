package librarystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
)

type importUpdateChapterRow struct {
	id                string
	title             string
	order             int
	revision          int64
	publishedRevision int64
	deleted           bool
}

// The author updates a book from a new file of the same volume
// (specs/2026-10-27-update-book-from-file.md, UC1).
func TestLibraryStoreFinalizeImportUpdatesBookInPlace(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := "00000000-0000-4000-8000-000000000930"
	projectID := "00000000-0000-4000-8000-000000000931"
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	proof := insertProjectStoreUserWithSessionForTest(t, db, userID, "import-update@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Update", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000932", projectID, userID, projects.RoleEditor, now, false)
	store := New(db)

	runImport := func(jobID, key, title string, mode library.ImportMode, bookID string, chapters []library.FinalizeImportChapterInput) library.FinalizedImport {
		t.Helper()
		payload := []byte("<FictionBook>" + key + "</FictionBook>")
		digest := sha256.Sum256(payload)
		if _, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: proof,
			IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
			RequestFingerprint: key + "-fingerprint", FileBytes: payload,
			Metadata: library.ImportFileMetadata{
				StorageKey: key, SourceFileName: key + ".fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
				SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
				ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
			},
		}); err != nil {
			t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
		}
		claimDecision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || claimDecision.Claim.JobID != jobID {
			t.Fatalf("ClaimNextQueuedJobForWorker(%s) = %+v, %v", key, claimDecision.Claim, err)
		}
		claim := claimDecision.Claim
		if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
			t.Fatalf("MarkImportParsingForClaim(%s) error = %v", key, err)
		}
		if err := store.MarkImportSavingForClaim(ctx, claim, len(chapters)); err != nil {
			t.Fatalf("MarkImportSavingForClaim(%s) error = %v", key, err)
		}
		finalized, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: key,
			ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: title, BookID: bookID, ImportMode: mode,
			SourceFileName: key + ".fb2", InitialPublication: library.InitialPublicationPublishImported, Chapters: chapters,
		})
		if err != nil {
			t.Fatalf("FinalizeImport(%s) error = %v", key, err)
		}
		return finalized
	}
	chapter := func(title string, paragraphs ...string) library.FinalizeImportChapterInput {
		input := library.FinalizeImportChapterInput{Title: title}
		for _, paragraph := range paragraphs {
			text := paragraph
			input.Paragraphs = append(input.Paragraphs, library.ParagraphInput{Markdown: text, Text: &text})
		}
		return input
	}
	rows := func(bookID string) map[string]importUpdateChapterRow {
		t.Helper()
		result, err := db.Pool.Query(ctx, `
			SELECT id::text, title, "order", revision, COALESCE(published_revision, 0), deleted_at IS NOT NULL
			FROM chapters WHERE project_id = $1 AND book_id = $2
		`, projectID, bookID)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		byTitle := map[string]importUpdateChapterRow{}
		for result.Next() {
			var row importUpdateChapterRow
			if err := result.Scan(&row.id, &row.title, &row.order, &row.revision, &row.publishedRevision, &row.deleted); err != nil {
				t.Fatal(err)
			}
			byTitle[row.title] = row
		}
		return byTitle
	}

	first := runImport("00000000-0000-4000-8000-000000000933", "update-first", "Том первый", library.ImportModeNewBook, "", []library.FinalizeImportChapterInput{
		chapter("Глава 1", "Один."), chapter("Глава 2", "Два."), chapter("Глава 3", "Три.", "Ещё три."), chapter("Глава 5", "Пять."),
	})
	before := rows(first.BookID)
	var bookOrderBefore int
	if err := db.Pool.QueryRow(ctx, `SELECT "order" FROM books WHERE id = $1`, first.BookID).Scan(&bookOrderBefore); err != nil {
		t.Fatal(err)
	}

	second := runImport("00000000-0000-4000-8000-000000000934", "update-second", "Другое название из файла", library.ImportModeUpdateBook, first.BookID, []library.FinalizeImportChapterInput{
		chapter("Глава 1", "Один."), chapter("Глава 2", "Два, исправлено."), chapter("Глава третья", "Три.", "Ещё три."), chapter("Глава 4", "Четыре."),
	})
	if second.BookID != first.BookID || second.ImportMode != library.ImportModeUpdateBook {
		t.Fatalf("update result = %+v, want book %s in update mode", second, first.BookID)
	}
	// specs/2026-10-27-update-book-from-file.md UC4: «Глава третья» keeps its text and is counted as renamed.
	if second.UnchangedChapters != 1 || second.ChangedChapters != 1 || second.RenamedChapters != 1 || second.AddedChapters != 1 || second.RemovedChapters != 1 {
		t.Fatalf("update counters = %+v, want unchanged 1, changed 1, renamed 1, added 1, removed 1", second)
	}

	after := rows(first.BookID)
	same, edited, renamed, added, removed := after["Глава 1"], after["Глава 2"], after["Глава третья"], after["Глава 4"], after["Глава 5"]
	if same.id != before["Глава 1"].id || same.revision != before["Глава 1"].revision || same.publishedRevision != same.revision {
		t.Fatalf("unchanged chapter = %+v, before %+v", same, before["Глава 1"])
	}
	if edited.id != before["Глава 2"].id || edited.revision != before["Глава 2"].revision+1 || edited.publishedRevision != edited.revision {
		t.Fatalf("changed chapter = %+v, before %+v", edited, before["Глава 2"])
	}
	if renamed.id != before["Глава 3"].id || renamed.revision != before["Глава 3"].revision || renamed.deleted {
		t.Fatalf("renamed chapter = %+v, before %+v", renamed, before["Глава 3"])
	}
	if added.id == "" || added.deleted || added.publishedRevision != added.revision {
		t.Fatalf("added chapter = %+v", added)
	}
	if removed.id != before["Глава 5"].id || !removed.deleted {
		t.Fatalf("removed chapter = %+v, before %+v", removed, before["Глава 5"])
	}
	if got := fmt.Sprint(same.order, edited.order, renamed.order, added.order); got != "1 2 3 4" {
		t.Fatalf("chapter order = %s, want the order of the file", got)
	}

	var bookTitle string
	var bookOrder int
	if err := db.Pool.QueryRow(ctx, `SELECT title, "order" FROM books WHERE id = $1 AND deleted_at IS NULL`, first.BookID).Scan(&bookTitle, &bookOrder); err != nil {
		t.Fatal(err)
	}
	if bookTitle != "Том первый" || bookOrder != bookOrderBefore {
		t.Fatalf("book = %q at %d, want the same title and place", bookTitle, bookOrder)
	}
	for _, variant := range []string{"draft", "published"} {
		var text string
		if err := db.Pool.QueryRow(ctx, `
			SELECT string_agg(plain_text, ' ' ORDER BY "order") FROM chapter_paragraphs
			WHERE chapter_id = $1 AND revision = $2 AND variant = $3
		`, edited.id, edited.revision, variant).Scan(&text); err != nil {
			t.Fatal(err)
		}
		if text != "Два, исправлено." {
			t.Fatalf("%s text of the changed chapter = %q", variant, text)
		}
	}
	var tombstones int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM deletion_tombstones WHERE project_id = $1 AND entity_kind = 'chapter' AND chapter_id = $2
	`, projectID, removed.id).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 1 {
		t.Fatalf("tombstones of the removed chapter = %d, want 1 (restorable from «Недавно удалённые»)", tombstones)
	}
	var resultMode string
	var resultChanged int
	if err := db.Pool.QueryRow(ctx, `
		SELECT result_json->>'importMode', (result_json->>'changedChapters')::int FROM jobs WHERE id = '00000000-0000-4000-8000-000000000934'
	`).Scan(&resultMode, &resultChanged); err != nil {
		t.Fatal(err)
	}
	if resultMode != string(library.ImportModeUpdateBook) || resultChanged != 1 {
		t.Fatalf("job result = %s, changed %d", resultMode, resultChanged)
	}
	assertNoActiveImportOrderConflict(t, db, projectID)

	// The update dialog promises that a removed chapter comes back from «Недавно удалённые»,
	// also in a project with chats: they point at the source view the restore rebuilds.
	chatID := "00000000-0000-4000-8000-000000000939"
	clearChat := func() { _, _ = db.Pool.Exec(context.Background(), `DELETE FROM chat_sessions WHERE id = $1`, chatID) }
	clearChat()
	t.Cleanup(clearChat)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title)
		VALUES ($1, $2, 'published', 'project', 'Чат саги')
	`, chatID, projectID); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreChapter(ctx, library.RestoreChapterStoreInput{
		ActorUserID: userID, ProjectID: projectID, BookID: first.BookID, ChapterID: removed.id,
	})
	if err != nil {
		t.Fatalf("RestoreChapter(removed by update) error = %v", err)
	}
	if restored.ID != removed.id || restored.DeletedAt != nil {
		t.Fatalf("RestoreChapter(removed by update) = %#v", restored)
	}
}

// An update names a live book of the same project.
func TestLibraryStoreImportUpdateRejectsForeignOrDeletedBook(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := "00000000-0000-4000-8000-000000000940"
	projectID := "00000000-0000-4000-8000-000000000941"
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	proof := insertProjectStoreUserWithSessionForTest(t, db, userID, "import-update-foreign@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Import Update Foreign", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000942", projectID, userID, projects.RoleOwner, now, false)
	payload := []byte("<FictionBook>foreign</FictionBook>")
	digest := sha256.Sum256(payload)
	_, err := New(db).CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
		JobID: "00000000-0000-4000-8000-000000000943", ProjectID: projectID, ActorUserID: userID, SessionWriteProof: proof,
		IdempotencyKey: "update-foreign", RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
		RequestFingerprint: "update-foreign", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: "update-foreign", SourceFileName: "foreign.fb2", Extension: ".fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: "update-foreign",
			ImportMode: library.ImportModeUpdateBook, TargetBookID: "00000000-0000-4000-8000-0000000009ff",
			InitialPublication: library.InitialPublicationPublishImported,
		},
	})
	if err != library.ErrNotFound {
		t.Fatalf("update of an unknown book error = %v, want ErrNotFound", err)
	}
	assertImportTreeCounts(t, db, projectID, 0, 0, 0)
}

func assertNoActiveImportOrderConflict(t *testing.T, db *storedb.DB, projectID string) {
	t.Helper()
	var active int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'import' AND status IN ('queued', 'running', 'canceling')
	`, projectID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active import jobs = %d, want 0", active)
	}
}
