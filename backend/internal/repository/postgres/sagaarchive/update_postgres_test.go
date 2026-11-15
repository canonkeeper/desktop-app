// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	librarystore "canonkeeper/backend/internal/store/library"
)

// updateBook updates a book from a file of its volume as «Обновить из файла» does: the upload of the file, its import Job
// taken by the worker, the chapters of the file read and the book rewritten by the import of the product.
func (world *world) updateBook(projectID, bookID string, chapters []library.FinalizeImportChapterInput) library.FinalizedImport {
	world.t.Helper()
	store := librarystore.New(world.db)
	payload := []byte("Том первый. Перевал.")
	sum := sha256.Sum256(payload)
	key, jobID := "update-of-the-book", ""
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT gen_random_uuid()::text`).Scan(&jobID); err != nil {
		world.t.Fatal(err)
	}
	if _, err := store.CreateImportStagingForActor(world.ctx, library.CreateImportStagingForActorInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: world.user, SessionWriteProof: world.proof,
		IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
		RequestFingerprint: key + "-fingerprint", FileBytes: payload,
		Metadata: library.ImportFileMetadata{
			StorageKey: key, SourceFileName: "perevaL.txt", Extension: ".txt", MIMEType: "text/plain",
			SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(sum[:]), IdempotencyKey: key,
			ImportMode: library.ImportModeUpdateBook, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
		},
	}); err != nil {
		world.t.Fatalf("the upload of the file of the book: %v", err)
	}
	claim := world.claim(jobs.KindImport)
	if claim.JobID != jobID {
		world.t.Fatalf("the worker took %s, the update is %s", claim.JobID, jobID)
	}
	if err := store.MarkImportParsingForClaim(world.ctx, claim); err != nil {
		world.t.Fatal(err)
	}
	if err := store.MarkImportSavingForClaim(world.ctx, claim, len(chapters)); err != nil {
		world.t.Fatal(err)
	}
	finalized, err := store.FinalizeImport(world.ctx, library.FinalizeImportStoreInput{
		JobID: jobID, ProjectID: projectID, ActorUserID: world.user, StorageKey: key, ExpectedClaimIncarnation: claim.ClaimIncarnation,
		BookTitle: "Перевал", BookID: bookID, ImportMode: library.ImportModeUpdateBook, SourceFileName: "perevaL.txt",
		InitialPublication: library.InitialPublicationPublishImported, Chapters: chapters,
	})
	if err != nil {
		world.t.Fatalf("the update of the book from its file: %v", err)
	}
	return finalized
}

func fileChapter(title string, texts ...string) library.FinalizeImportChapterInput {
	chapter := library.FinalizeImportChapterInput{Title: title}
	for index, text := range texts {
		plain := domain.PlainText(text)
		chapter.Paragraphs = append(chapter.Paragraphs, library.ParagraphInput{Kind: library.ParagraphKindParagraph, Order: index + 1, Markdown: text, Text: &plain})
	}
	return chapter
}

// specs/2026-11-13-desk-sagafile.md SF3.14: «Обновить из файла» of a book of an opened saga — a book whose chapters keep the
// orders the file brought, a gap in the draft and both «Глава 5» — goes by the import of the product: it changes the chapter
// whose words changed, adds the new one, keeps the others, keeps the update to return for seven days, and the saga is read and
// saved again.
func TestABookOfAnOpenedSagaIsUpdatedFromItsFile(t *testing.T) {
	world := newWorld(t, "ck_saga_file_update")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "update-key")
	world.ready(opened.ProjectID)
	var bookID string
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT id::text FROM books WHERE project_id = $1 AND title = 'Перевал'`, opened.ProjectID).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	finalized := world.updateBook(opened.ProjectID, bookID, []library.FinalizeImportChapterInput{
		fileChapter("Глава 5. Перевал", "Мара поднялась к перевалу на рассвете.", "Ветер нёс *снег* и **соль**."),
		fileChapter("Застава", "На заставе Тарен ждал её пятый день.", "Огонь почти погас."),
		fileChapter("Метель", "Метель шла двое суток."),
		fileChapter("Буран", "Буран пришёл с моря."),
	})
	if finalized.UnchangedChapters != 2 || finalized.ChangedChapters != 1 || finalized.AddedChapters != 1 || finalized.RemovedChapters != 0 {
		t.Fatalf("the update = %+v; want two chapters kept, one changed, one added", finalized)
	}
	if snapshots := world.count(`SELECT count(*) FROM book_update_snapshots WHERE book_id = $1`, bookID); snapshots != 1 {
		t.Fatalf("the snapshots of the update = %d; want the book to return for seven days", snapshots)
	}
	world.index(opened.ProjectID, governance.SourceViewPublished)
	published := world.keys(opened.ProjectID, governance.SourceViewPublished)
	want := []string{"b2c0 Глава 5. Маяк", "b2c1 Смотритель", "b1c0 Глава 5. Перевал", "b1c1 Застава", "b1c2 Метель", "b1c3 Буран"}
	if !slices.Equal(published, want) {
		t.Fatalf("the published text after the update = %v\nwant %v", published, want)
	}
	archive := read(t, world.save(opened.ProjectID))
	var changed bool
	if err := archive.EachChapter(func(chapter domain.Chapter) error {
		for _, paragraph := range chapter.Paragraphs {
			changed = changed || strings.Contains(paragraph.Markdown, "пятый день")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !changed || len(archive.Chapters) != 7 {
		t.Fatalf("the next file of the saga = %d chapters, the changed text %v", len(archive.Chapters), changed)
	}
}
