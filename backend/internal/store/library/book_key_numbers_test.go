package librarystore

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	librarydomain "canonkeeper/backend/internal/domain/library"
	notesdomain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/domain/quota"
	memorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	notesrepo "canonkeeper/backend/internal/repository/postgres/notes"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	trashrepo "canonkeeper/backend/internal/repository/postgres/trash"
	notesservice "canonkeeper/backend/internal/services/notes"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
	"canonkeeper/backend/internal/services/sagareference"
)

// The keys of the chapters of a saga on the real path of its books (specs/2026-11-11-ux7-reorder.md RK1): a book gets
// its number once, the deletion, the return and the move of the books around it leave the keys of its chapters alone,
// and a new book gets a number no book of the saga had.

// keysOf reads the published text of the saga as the tools and the pages read it: the key of every chapter by its id.
func (fixture *readyWaveFixture) keysOf() (map[string]string, *memorydomain.Version) {
	fixture.t.Helper()
	saga := sagarepo.New(fixture.db)
	version, err := memoryservice.NewSaga(saga, saga).ChapterVersion(fixture.ctx, fixture.projectID, "published")
	if err != nil {
		fixture.t.Fatalf("ChapterVersion() error = %v", err)
	}
	keys := map[string]string{}
	for _, chapter := range version.Chapters {
		keys[chapter.ID] = chapter.Key
	}
	return keys, version
}

func (fixture *readyWaveFixture) chaptersOfBook(bookID string) []string {
	fixture.t.Helper()
	rows, err := fixture.db.Pool.Query(fixture.ctx, `SELECT id::text FROM chapters WHERE book_id = $1 ORDER BY "order"`, bookID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			fixture.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// assertKeys checks that every chapter of the book has a key of the number of the book and, when keys were seen
// before, the same keys; it returns the keys.
func (fixture *readyWaveFixture) assertKeys(step, bookID string, number int, before []string) []string {
	fixture.t.Helper()
	keys, _ := fixture.keysOf()
	chapters := fixture.chaptersOfBook(bookID)
	if len(chapters) == 0 {
		fixture.t.Fatalf("%s: the book has no chapters", step)
	}
	got := make([]string, len(chapters))
	for i, id := range chapters {
		got[i] = keys[id]
		if !strings.HasPrefix(got[i], fmt.Sprintf("b%dc", number)) || before != nil && got[i] != before[i] {
			fixture.t.Fatalf("%s: chapter %d of the book has key %q, want one of the number %d as before %v (all: %v)", step, i+1, got[i], number, before, keys)
		}
	}
	return got
}

func (fixture *readyWaveFixture) deleteBook(bookID string) {
	fixture.t.Helper()
	if _, err := fixture.store.FenceBookDeletion(fixture.ctx, library.FenceBookDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: bookID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		fixture.t.Fatalf("FenceBookDeletion() error = %v", err)
	}
}

func (fixture *readyWaveFixture) restoreBook(bookID string) {
	fixture.t.Helper()
	if _, err := fixture.store.RestoreBook(fixture.ctx, library.RestoreBookStoreInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: bookID,
	}); err != nil {
		fixture.t.Fatalf("RestoreBook() error = %v", err)
	}
}

func (fixture *readyWaveFixture) moveBook(bookID string, after *string) library.Book {
	fixture.t.Helper()
	book, err := fixture.store.GetBook(fixture.ctx, fixture.projectID, bookID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	moved, err := fixture.store.UpdateBook(fixture.ctx, library.UpdateBookStoreInput{
		ProjectID: fixture.projectID, BookID: bookID, Title: book.Title, Subtitle: book.Subtitle, CoverColor: book.CoverColor,
		UpdatedAt: time.Now(), AfterBookID: library.OrderPlacement{Present: true, AfterID: after},
	})
	if err != nil {
		fixture.t.Fatalf("UpdateBook(move) error = %v", err)
	}
	return moved
}

func (fixture *readyWaveFixture) shelf() []string {
	fixture.t.Helper()
	rows, err := fixture.db.Pool.Query(fixture.ctx, `
		SELECT title || ':' || "order" FROM books WHERE project_id = $1 AND deleted_at IS NULL ORDER BY "order", id
	`, fixture.projectID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer rows.Close()
	var shelf []string
	for rows.Next() {
		var place string
		if err := rows.Scan(&place); err != nil {
			fixture.t.Fatal(err)
		}
		shelf = append(shelf, place)
	}
	return shelf
}

// RK1.1: in a saga of three books the second goes — the keys of the third stay; it comes back — its keys are its old
// ones; a new book stands after the books of the shelf (specs/2026-11-11-ux7-upload.md UP2.3) with a number of its own.
func TestTheKeysOfAChapterOutliveTheDeletionAndTheReturnOfTheBooksAroundIt(t *testing.T) {
	fixture := newReadyWaveFixture(t, "d71")
	first := fixture.load(testUUID(71_001), fixture.ownerID, "keys-first", "Том 1", library.ImportModeNewBook, "", "первая, начало", "первая, конец")
	second := fixture.load(testUUID(71_002), fixture.ownerID, "keys-second", "Том 2", library.ImportModeNewBook, "", "вторая, начало", "вторая, конец")
	third := fixture.load(testUUID(71_003), fixture.ownerID, "keys-third", "Том 3", library.ImportModeNewBook, "", "третья, начало", "третья, конец")
	fixture.settle()
	firstKeys := fixture.assertKeys("three books", first.BookID, 1, nil)
	secondKeys := fixture.assertKeys("three books", second.BookID, 2, nil)
	thirdKeys := fixture.assertKeys("three books", third.BookID, 3, nil)

	fixture.deleteBook(second.BookID)
	fixture.assertKeys("the second deleted", third.BookID, 3, thirdKeys)
	fixture.assertKeys("the second deleted", first.BookID, 1, firstKeys)

	fixture.restoreBook(second.BookID)
	fixture.settle()
	fixture.assertKeys("the second back", second.BookID, 2, secondKeys)
	fixture.assertKeys("the second back", third.BookID, 3, thirdKeys)

	// The second goes again; a new book stands after the books of the shelf — «Книга 3» — with keys of the number 4,
	// and the place the deleted one keeps stays for its return.
	fixture.deleteBook(second.BookID)
	fourth := fixture.load(testUUID(71_004), fixture.ownerID, "keys-fourth", "Том 4", library.ImportModeNewBook, "", "четвёртая")
	fixture.settle()
	fourthKeys := fixture.assertKeys("a new book", fourth.BookID, 4, nil)
	if shelf := strings.Join(fixture.shelf(), " "); shelf != "Том 1:1 Том 3:3 Том 4:4" {
		t.Fatalf("the shelf with a new book after the books = %s", shelf)
	}
	_, version := fixture.keysOf()
	if chapter, ok := version.Find(fourthKeys[0]); !ok || !strings.HasPrefix(chapter.Place(), "Книга 3, ") {
		t.Fatalf("the place of the new book = %+v, %v; want «Книга 3»", chapter, ok)
	}
	// The second comes back to its place, and nobody's keys change.
	fixture.restoreBook(second.BookID)
	fixture.settle()
	if shelf := strings.Join(fixture.shelf(), " "); shelf != "Том 1:1 Том 2:2 Том 3:3 Том 4:4" {
		t.Fatalf("the shelf with the second back at its place = %s", shelf)
	}
	fixture.assertKeys("the second back again", second.BookID, 2, secondKeys)
	fixture.assertKeys("the second back again", fourth.BookID, 4, fourthKeys)
	fixture.assertKeys("the second back again", third.BookID, 3, thirdKeys)
}

// RK1.3, RK1.4: after the third book is put first, a kept link of an answer, the chapter of a drawing and the link of a
// thread — kept keys all — lead to the same chapters, and the place names the new place of the book.
func TestAMoveOfABookKeepsTheKeysAndMovesThePlaces(t *testing.T) {
	fixture := newReadyWaveFixture(t, "d72")
	fixture.load(testUUID(72_001), fixture.ownerID, "move-first", "Том 1", library.ImportModeNewBook, "", "первая, начало")
	second := fixture.load(testUUID(72_002), fixture.ownerID, "move-second", "Том 2", library.ImportModeNewBook, "", "вторая, начало")
	third := fixture.load(testUUID(72_003), fixture.ownerID, "move-third", "Том 3", library.ImportModeNewBook, "", "третья, начало", "третья, конец")
	fixture.settle()
	before, version := fixture.keysOf()
	last := fixture.chaptersOfBook(third.BookID)[1]
	answer := "Порт закрыт [т. 3, «Глава. третья, конец» ¶1](source:" + before[last] + "/1)."
	chapter, _ := version.Find(before[last])
	if got := memorydomain.PlainLinks(answer, version.Places(memorydomain.LinkedKeys(answer))); !strings.HasPrefix(before[last], "b3c") ||
		got != fmt.Sprintf("Порт закрыт Книга 3, гл. %d „Глава. третья, конец“, абзац 1.", chapter.Order) {
		t.Fatalf("the link before the move reads %q", got)
	}

	moved := fixture.moveBook(third.BookID, nil)
	if moved.Order != 1 {
		t.Fatalf("the moved book stands at %d, want 1", moved.Order)
	}
	if shelf := strings.Join(fixture.shelf(), " "); shelf != "Том 3:1 Том 1:2 Том 2:3" {
		t.Fatalf("the shelf after the move = %s", shelf)
	}
	after, version := fixture.keysOf()
	for id, key := range before {
		if after[id] != key {
			t.Fatalf("chapter %s had key %q and has %q after the move", id, key, after[id])
		}
	}
	if version.Chapters[0].BookID != third.BookID {
		t.Fatalf("the saga reads first %+v, want the book put first", version.Chapters[0])
	}
	if got := memorydomain.PlainLinks(answer, version.Places(memorydomain.LinkedKeys(answer))); got != fmt.Sprintf("Порт закрыт Книга 1, гл. %d „Глава. третья, конец“, абзац 1.", chapter.Order) {
		t.Fatalf("the link after the move reads %q", got)
	}
	// The same move again leaves the shelf as it is.
	fixture.moveBook(third.BookID, nil)
	if shelf := strings.Join(fixture.shelf(), " "); shelf != "Том 3:1 Том 1:2 Том 2:3" {
		t.Fatalf("the shelf after the same move again = %s", shelf)
	}
	// A note of a place of the third on the shelf names it by the shelf too.
	note := fixture.noteOn(second.BookID, fixture.chaptersOfBook(second.BookID)[0])
	if place := fixture.notePlace(note); !strings.HasPrefix(place, "Книга 3, гл. ") {
		t.Fatalf("the note of «Том 2» reads %q, want «Книга 3»", place)
	}
	// RK1.6: the first book on the shelf goes; the two left are «Книга 1» and «Книга 2» on the shelf and in the places of the
	// chapters, though their stored orders keep the gap the deleted one comes back to.
	fixture.deleteBook(third.BookID)
	if place := fixture.notePlace(note); !strings.HasPrefix(place, "Книга 2, гл. ") {
		t.Fatalf("the note of «Том 2» without the first book reads %q, want «Книга 2»", place)
	}
	places := map[string]int{}
	books, err := fixture.store.ListBooks(fixture.ctx, library.ListBooksStoreInput{ProjectID: fixture.projectID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, book := range books {
		places[book.Title] = book.Place
	}
	if places["Том 1"] != 1 || places["Том 2"] != 2 || len(places) != 2 {
		t.Fatalf("the shelf without its first book = %v; want «Том 1» first", places)
	}
	got, err := fixture.store.GetBook(fixture.ctx, fixture.projectID, second.BookID)
	if err != nil || got.Place != 2 || got.Order != 3 {
		t.Fatalf("the second book = place %d, order %d, %v; want place 2 at its stored order 3", got.Place, got.Order, err)
	}
	_, version = fixture.keysOf()
	if !strings.HasPrefix(version.Chapters[0].Place(), "Книга 1, ") || version.Chapters[0].BookTitle != "Том 1" {
		t.Fatalf("the first chapter of the saga is %q of %q", version.Chapters[0].Place(), version.Chapters[0].BookTitle)
	}
	// «Недавно удалённые» name the deleted book by its title — a number of the shelf would be a second «Книга 1».
	deleted, err := trashrepo.New(fixture.db).ListDeletedSources(fixture.ctx, fixture.ownerID, 10)
	if err != nil || len(deleted.Books.Items) != 1 || deleted.Books.Items[0].Title != "Том 3" {
		t.Fatalf("the deleted book in «Недавно удалённые» = %+v, %v", deleted.Books, err)
	}
	// It comes back to its place: the first again, and the others step on.
	fixture.restoreBook(third.BookID)
	restored, err := fixture.store.GetBook(fixture.ctx, fixture.projectID, third.BookID)
	if err != nil || restored.Place != 1 {
		t.Fatalf("the book back = place %d, %v; want 1", restored.Place, err)
	}
	if place := fixture.notePlace(note); !strings.HasPrefix(place, "Книга 3, гл. ") {
		t.Fatalf("the note of «Том 2» with the first book back reads %q, want «Книга 3»", place)
	}
}

// noteOn saves the owner's note of the first paragraph of a chapter by the path of the reader.
func (fixture *readyWaveFixture) noteOn(bookID, chapterID string) string {
	fixture.t.Helper()
	var paragraphID, text string
	var revision int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT paragraph.id::text, paragraph.plain_text, chapter.published_revision
		FROM chapters AS chapter
		JOIN chapter_paragraphs AS paragraph ON paragraph.chapter_id = chapter.id
		  AND paragraph.revision = chapter.published_revision AND paragraph.variant = 'published'
		WHERE chapter.id = $1::uuid ORDER BY paragraph."order" LIMIT 1
	`, chapterID).Scan(&paragraphID, &text, &revision); err != nil {
		fixture.t.Fatal(err)
	}
	source := notesdomain.Source{Kind: "chapter", Quote: text, Locator: &librarydomain.ReaderLocator{
		ProjectID: fixture.projectID, BookID: bookID, ChapterID: chapterID, TargetView: "published", Revision: revision, ParagraphID: &paragraphID,
	}}
	note, err := notesservice.Create{Repository: notesrepo.New(fixture.db)}.Execute(fixture.ctx,
		notesservice.Write{UserID: fixture.ownerID, Proof: fixture.ownerProof}, notesservice.CreateInput{Body: "Сверить", Source: &source},
		"note-of-"+chapterID, "POST", "/personal-notes")
	if err != nil {
		fixture.t.Fatal(err)
	}
	return note.ID
}

// notePlace is the place the catalog of the notes names beside the note, which the note itself names alike.
func (fixture *readyWaveFixture) notePlace(noteID string) string {
	fixture.t.Helper()
	repo := notesrepo.New(fixture.db)
	page, err := notesservice.List{Repository: repo}.Execute(fixture.ctx, fixture.ownerID, notesservice.ListInput{ProjectID: fixture.projectID})
	if err != nil {
		fixture.t.Fatal(err)
	}
	listed := ""
	for _, entry := range page.Data {
		if entry.ID == noteID {
			listed = entry.Place
		}
	}
	resolved, err := repo.ReadSource(fixture.ctx, fixture.ownerID, noteID)
	if err != nil || resolved.Resolution.Place != listed {
		fixture.t.Fatalf("the note reads %q and the catalog %q, %v", resolved.Resolution.Place, listed, err)
	}
	return listed
}

// countingCardModel counts the calls of the cards Job, whose workers call it at once.
type countingCardModel struct {
	waveCardModel
	cards, names atomic.Int32
}

func (model *countingCardModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions == memorydomain.NameInstructions {
		model.names.Add(1)
	} else {
		model.cards.Add(1)
	}
	return model.waveCardModel.Complete(ctx, instructions, input)
}

// RK2.3, RK2.4: after a move the map is given as being rebuilt and its work is queued; the work asks the model for no
// card — the cards stay those of the texts of the chapters — and the day of the author counts nothing.
func TestAMoveOfABookRebuildsTheMapAndReadsNoBookAgain(t *testing.T) {
	fixture := newReadyWaveFixture(t, "d73")
	model := &countingCardModel{}
	saga := sagarepo.New(fixture.db).WithTextIndex(fixture.indexing)
	fixture.cards = memoryservice.NewBuildCards(saga, saga, model, nil, "test-model", 2, nil, nil).
		WithMap(memoryservice.NewMapBuilder(memoryservice.NewSaga(saga, saga), saga, sagamemorytest.Materials(nil)), func(_, stage string, err error) {
			t.Errorf("map %s: %v", stage, err)
		})
	first := fixture.load(testUUID(73_001), fixture.ownerID, "map-first", "Том 1", library.ImportModeNewBook, "", "Ольга вошла в порт", "Ольга ушла")
	second := fixture.load(testUUID(73_002), fixture.ownerID, "map-second", "Том 2", library.ImportModeNewBook, "", "Ольга вернулась")
	fixture.settle()
	getMap := sagareference.NewGetMap(saga, memoryservice.NewSaga(saga, saga), saga, saga)
	read := func(step string) (memorydomain.MapDocument, string) {
		t.Helper()
		result, err := getMap.Execute(fixture.ctx, sagareference.MapInput{Scope: sagareference.Scope{
			ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, View: "published",
		}})
		if err != nil {
			t.Fatalf("%s: GetMap() error = %v", step, err)
		}
		var document memorydomain.MapDocument
		if err := memorydomain.DecodeStored(result.Document, &document); err != nil {
			t.Fatalf("%s: decode the map: %v", step, err)
		}
		return document, result.Revision
	}
	if document, _ := read("the map built"); document.Building {
		t.Fatal("the map of a saga read through is being rebuilt")
	}
	var cardsBefore int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM chapter_cards WHERE project_id = $1`, fixture.projectID).Scan(&cardsBefore); err != nil {
		t.Fatal(err)
	}
	day := func() string {
		t.Helper()
		var usage string
		if err := fixture.db.Pool.QueryRow(fixture.ctx, `
			SELECT coalesce((SELECT string_agg(to_jsonb(counted)::text, ',') FROM writer_usage_days AS counted WHERE counted.user_id = $1), '') ||
			       '|' || coalesce((SELECT to_jsonb(usage)::text FROM subscription_usage AS usage WHERE usage.user_id = $1), '')
		`, fixture.ownerID).Scan(&usage); err != nil {
			t.Fatal(err)
		}
		return usage
	}
	usage := day()
	modelCards := model.cards.Load()

	fixture.moveBook(second.BookID, nil)
	document, _ := read("the move")
	if !document.Building {
		t.Fatal("the map after a move of a book is not marked as being rebuilt")
	}
	var queued int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = $2 AND status = 'queued'
	`, fixture.projectID, string(jobs.KindChapterCards)).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued cards Jobs after the move = %d, %v; want 1", queued, err)
	}
	var indexes int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind IN ('canon_text_index', 'import') AND status = 'queued'
	`, fixture.projectID).Scan(&indexes); err != nil || indexes != 0 {
		t.Fatalf("a move queued %d readings of the books (%v)", indexes, err)
	}
	fixture.settle()
	if calls := model.cards.Load(); calls != modelCards {
		t.Fatalf("the rebuild after a move asked the model for %d cards", calls-modelCards)
	}
	var cardsAfter int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM chapter_cards WHERE project_id = $1`, fixture.projectID).Scan(&cardsAfter); err != nil || cardsAfter != cardsBefore {
		t.Fatalf("cards after the move = %d, %v; want %d", cardsAfter, err, cardsBefore)
	}
	if after := day(); after != usage {
		t.Fatalf("the day of the author after a move = %s, want %s", after, usage)
	}
	rebuilt, _ := read("the map rebuilt")
	if rebuilt.Building || len(rebuilt.Chapters) != 3 || rebuilt.Chapters[0].BookID != second.BookID ||
		!strings.HasPrefix(rebuilt.Chapters[0].Label, "Книга 1, ") || rebuilt.Chapters[1].BookID != first.BookID {
		t.Fatalf("the map rebuilt after the move = %+v", rebuilt)
	}
}

// loadTitled imports one new book of the owner with chapters of the titles given.
func (fixture *readyWaveFixture) loadTitled(jobID, key, title string, chapters ...string) library.FinalizedImport {
	t := fixture.t
	t.Helper()
	fixture.stage(jobID, fixture.ownerID, key, library.ImportModeNewBook, "")
	decision, err := fixture.claims.ClaimNextQueuedJobForWorker(fixture.ctx, []jobs.Kind{jobs.KindImport})
	if err != nil || decision.Claim.JobID != jobID {
		t.Fatalf("import claim %s = %+v, %v", key, decision.Claim, err)
	}
	claim := decision.Claim
	if err := fixture.store.MarkImportParsingForClaim(fixture.ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkImportSavingForClaim(fixture.ctx, claim, len(chapters)); err != nil {
		t.Fatal(err)
	}
	input := library.FinalizeImportStoreInput{
		JobID: claim.JobID, ProjectID: fixture.projectID, ActorUserID: fixture.ownerID, StorageKey: key,
		ExpectedClaimIncarnation: claim.ClaimIncarnation, BookTitle: title, ImportMode: library.ImportModeNewBook,
		InitialPublication: library.InitialPublicationPublishImported, SourceFileName: key + ".fb2",
	}
	for _, chapter := range chapters {
		input.Chapters = append(input.Chapters, library.FinalizeImportChapterInput{
			Title: chapter, Paragraphs: []library.ParagraphInput{{Markdown: chapter + ": один."}, {Markdown: "Два."}, {Markdown: "Три."}},
		})
	}
	finalized, err := fixture.store.FinalizeImport(fixture.ctx, input)
	if err != nil {
		t.Fatalf("FinalizeImport(%s) error = %v", key, err)
	}
	return finalized
}

// RK1.5: a saga of one book gave its «Глава 2» the bare key «2», and an answer, a drawing and the voice kept it. A second
// volume with its own «Глава 2» makes the number ambiguous — the new keys are bKcM — and every kept «2» still leads to the
// «Глава 2» of the first book, by the same reading the chat, its source panel, the drawings and the contents do; after a
// move of the volumes, and after the second goes and comes back, too.
func TestABareChapterNumberKeptBeforeASecondVolumeLeadsToTheSameChapter(t *testing.T) {
	fixture := newReadyWaveFixture(t, "d74")
	first := fixture.loadTitled(testUUID(74_001), "bare-first", "Отмель", "Глава 1. Туман", "Глава 2. Вырванные страницы", "Глава 3. Пепел")
	fixture.settle()
	saga := sagarepo.New(fixture.db)
	memory := memoryservice.NewSaga(saga, saga)
	contents := sagareference.NewGetContents(saga, memory)
	answer := "Страницы вырваны [гл. 2 ¶2–3](source:2/2-3)."
	kept, ok, err := memory.FindChapter(fixture.ctx, fixture.projectID, "published", "2")
	if err != nil || !ok || kept.Key != "2" || kept.BookID != first.BookID {
		t.Fatalf("«2» in a saga of one book = %+v, %v, %v", kept, ok, err)
	}
	leads := func(step, shelfPlace string) {
		t.Helper()
		// The chat, its source panel (resolveChatChapterKey) and the chat tools read a key by FindChapter.
		chapter, ok, err := memory.FindChapter(fixture.ctx, fixture.projectID, "published", "2")
		if err != nil || !ok || chapter.ID != kept.ID {
			t.Fatalf("%s: «2» = %+v, %v, %v; want the «Глава 2» of the first book", step, chapter, ok, err)
		}
		// The links of an answer, the places of a drawing and of its references are named by Places.
		places, err := memory.Places(fixture.ctx, fixture.projectID, memorydomain.LinkedKeys(answer))
		want := "Страницы вырваны " + shelfPlace + ", гл. 2 „Вырванные страницы“, абзацы 2–3."
		if got := memorydomain.PlainLinks(answer, places); err != nil || got != want {
			t.Fatalf("%s: the answer reads %q, %v; want %q", step, got, err, want)
		}
		// The reader, the drawings and the voice open a kept key through the contents.
		result, err := contents.Execute(fixture.ctx, sagareference.ContentsInput{
			Scope: sagareference.Scope{ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, View: "published"}, Chapter: "2",
		})
		if err != nil || result.Found == nil || result.Found.ID != kept.ID || result.Book == nil || result.Book.ID != first.BookID {
			t.Fatalf("%s: the contents of «2» = %+v, %v", step, result.Found, err)
		}
	}
	leads("one book", "Книга 1")

	second := fixture.loadTitled(testUUID(74_002), "bare-second", "Снова", "Глава 1. Дорога", "Глава 2. Снова дорога", "Глава 3. Конец")
	fixture.settle()
	_, version := fixture.keysOf()
	bookKey := ""
	for _, chapter := range version.Chapters {
		if chapter.Key == "2" {
			t.Fatalf("an ambiguous number is the key of %+v", chapter)
		}
		if chapter.ID == kept.ID {
			bookKey = chapter.Key
		}
	}
	leads("a second volume", "Книга 1")

	fixture.moveBook(second.BookID, nil)
	leads("the second volume put first", "Книга 2")

	fixture.deleteBook(second.BookID)
	leads("the second volume deleted", "Книга 1")
	// bookKey was the key of the chapter while the two volumes were there; the links that kept it lead to it still.
	if chapter, ok, err := memory.FindChapter(fixture.ctx, fixture.projectID, "published", bookKey); err != nil || !ok || chapter.ID != kept.ID || !strings.HasPrefix(bookKey, "b1c") {
		t.Fatalf("%s without the second volume = %+v, %v, %v", bookKey, chapter, ok, err)
	}
	fixture.restoreBook(second.BookID)
	fixture.settle()
	leads("the second volume back", "Книга 2")
}
