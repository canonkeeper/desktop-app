package librarystore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	"canonkeeper/backend/internal/domain/quota"
	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	memorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	layerservice "canonkeeper/backend/internal/services/sagalayer"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
)

// The case of 05.11 (specs/2026-10-27-update-book-from-file.md, UC3–UC6) on generated files, through the text
// index, the cards Job and the queue of the thread layer: a book loaded from FB2 gets its cards and threads; then a
// Word file of the same volume with one word changed, spaces and no-break spaces, a renamed chapter and a chapter
// with other signs is previewed and loaded. The preview tells what the update does and writes nothing; the update
// changes one chapter by its words, renames one, carries the card of the signs, builds one new card and leaves the
// threads alone (R5.1, R3.3).
func TestLibraryStoreUpdateFromFileChangesWhatTheAuthorChanged(t *testing.T) {
	runUpdateScenario(t, false)
}

// The same file without one chapter: the chapter goes to «Недавно удалённые», and its deletion
// scrubs the thread layer of the saga (specs/2026-11-01-index-foundation.md, R2.8), so the threads are built anew;
// the preview says so before the update.
func TestLibraryStoreUpdateFromFileWithoutAChapterRebuildsTheThreads(t *testing.T) {
	runUpdateScenario(t, true)
}

func runUpdateScenario(t *testing.T, removal bool) {
	t.Helper()
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := "00000000-0000-4000-8000-000000000960"
	projectID := "00000000-0000-4000-8000-000000000961"
	cleanupLibraryImportStoreRows(t, db, userID, projectID)
	proof := insertProjectStoreUserWithSessionForTest(t, db, userID, "import-update-scenario@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Сага автора", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000962", projectID, userID, projects.RoleOwner, now, false)
	store := New(db)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	saga := sagarepo.New(db)
	model := &scenarioCardModel{}
	cards := memoryservice.NewBuildCards(saga, saga, model, nil, "test-model", 2, nil, nil)

	runImport := func(jobID, key string, mode library.ImportMode, bookID string, format bookexport.Format, payload []byte) library.FinalizedImport {
		t.Helper()
		digest := sha256.Sum256(payload)
		extension, mime := ".fb2", "application/x-fictionbook+xml"
		if format == bookexport.FormatDOCX {
			extension, mime = ".docx", format.MediaType()
		}
		if _, err := store.CreateImportStagingForActor(ctx, library.CreateImportStagingForActorInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, SessionWriteProof: proof,
			IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
			RequestFingerprint: key + "-fingerprint", FileBytes: payload,
			Metadata: library.ImportFileMetadata{
				StorageKey: key, SourceFileName: "tom-13" + extension, Extension: extension, MIMEType: mime,
				SizeBytes: int64(len(payload)), FileSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: key,
				ImportMode: mode, TargetBookID: bookID, InitialPublication: library.InitialPublicationPublishImported,
			},
		}); err != nil {
			t.Fatalf("CreateImportStagingForActor(%s) error = %v", key, err)
		}
		decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || decision.Claim.JobID != jobID {
			t.Fatalf("import claim %s = %+v, %v", key, decision.Claim, err)
		}
		chapters := parsedScenarioChapters(t, format, payload)
		if err := store.MarkImportParsingForClaim(ctx, decision.Claim); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkImportSavingForClaim(ctx, decision.Claim, len(chapters)); err != nil {
			t.Fatal(err)
		}
		finalized, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
			JobID: jobID, ProjectID: projectID, ActorUserID: userID, StorageKey: key,
			ExpectedClaimIncarnation: decision.Claim.ClaimIncarnation, BookTitle: "Том 3. Черновик", BookID: bookID, ImportMode: mode,
			SourceFileName: "tom-13" + extension, InitialPublication: library.InitialPublicationPublishImported, Chapters: chapters,
		})
		if err != nil {
			t.Fatalf("FinalizeImport(%s) error = %v", key, err)
		}
		return finalized
	}
	// The draft text index of the version and the cards Job it queues, with a model that counts the chapters it reads.
	indexAndCard := func() jobs.ChapterCardsResult {
		t.Helper()
		textClaim, textSubject := claimDraftTextIndexForTest(t, db, projectID)
		indexCurrentTextForTest(t, indexing, textClaim)
		if err := indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
			t.Fatal(err)
		}
		decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
		if err != nil {
			t.Fatalf("cards claim: %v", err)
		}
		subject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: governance.SourceViewDraft}
		if status, err := cards.RunForClaim(ctx, decision.Claim, subject, true); err != nil || status != jobs.StatusSucceeded {
			t.Fatalf("cards Job = %q, %v", status, err)
		}
		var raw []byte
		if err := db.Pool.QueryRow(ctx, `SELECT result_json FROM jobs WHERE id = $1`, decision.Claim.JobID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		result, err := jobs.DecodeResult(jobs.KindChapterCards, raw)
		if err != nil {
			t.Fatal(err)
		}
		return result.(jobs.ChapterCardsResult)
	}
	queueLayer := layerservice.NewQueueDue(saga)

	// The book of 31.08: an FB2, whose import collapses spaces, with cards and a finished thread layer.
	first := runImport("00000000-0000-4000-8000-000000000963", "scenario-first", library.ImportModeNewBook, "", bookexport.FormatFB2,
		scenarioFile(t, bookexport.FormatFB2, scenarioBook))
	if built := indexAndCard(); built.Built != len(scenarioBook) {
		t.Fatalf("first cards = %+v, want a card for each of %d chapters", built, len(scenarioBook))
	}
	if queued, err := queueLayer.Execute(ctx, projectID, "draft"); err != nil || !queued {
		t.Fatalf("first layer queued = %v, %v; want the first build", queued, err)
	}
	finishScenarioLayer(t, claims, saga, projectID)
	before := scenarioChapters(t, db, first.BookID)

	// The file of 05.11: Word with no-break spaces, one word, a new title, other signs, one chapter less.
	updateChapters := scenarioUpdate
	if !removal {
		updateChapters = append(append([]scenarioChapter(nil), scenarioUpdate...), scenarioBook[5])
	}
	update := scenarioFile(t, bookexport.FormatDOCX, updateChapters)
	preview := runImport("00000000-0000-4000-8000-000000000964", "scenario-preview", library.ImportModeUpdatePreview, first.BookID, bookexport.FormatDOCX, update)
	wantCounts := library.FinalizedImport{UnchangedChapters: 3, ChangedChapters: 1, PunctuationChapters: 1, RenamedChapters: 1}
	if removal {
		wantCounts.UnchangedChapters, wantCounts.RemovedChapters = 2, 1
	}
	if got := scenarioCounts(preview); got != wantCounts || preview.Preview == nil {
		t.Fatalf("preview counts = %+v, want %+v with a preview", got, wantCounts)
	}
	if got := scenarioChapters(t, db, first.BookID); !scenarioSameChapters(got, before) {
		t.Fatalf("chapters after the preview = %+v, want them untouched: %+v", got, before)
	}
	if preview.Preview.NewCards != 1 || preview.Preview.Threads != removal {
		t.Fatalf("preview estimate = %d new cards, threads %v; want one card, threads %v", preview.Preview.NewCards, preview.Preview.Threads, removal)
	}
	chapters := preview.Preview.Chapters
	if !removal {
		chapters = append(chapters, jobs.ImportPreviewChapter{Order: 6, Title: "Глава 6. Записка", Change: jobs.ImportPreviewRemoved})
	}
	if len(chapters) != 4 ||
		chapters[0].Order != 1 || chapters[0].Change != jobs.ImportPreviewWords || len(chapters[0].Places) != 1 ||
		!strings.Contains(chapters[0].Places[0].Before, "пятнадцати") || !strings.Contains(chapters[0].Places[0].After, "тринадцати") ||
		chapters[1].Order != 3 || chapters[1].Change != jobs.ImportPreviewRenamed || chapters[1].Title != "Глава 3. Мост" || chapters[1].PreviousTitle != "Глава 3. Переправа" ||
		chapters[2].Order != 4 || chapters[2].Change != jobs.ImportPreviewPunctuation || len(chapters[2].Places) == 0 ||
		chapters[3].Order != 6 || chapters[3].Change != jobs.ImportPreviewRemoved || chapters[3].Title != "Глава 6. Записка" {
		t.Fatalf("preview chapters = %+v", chapters)
	}
	var previewSubject, scrubbed bool
	var tombstones, textIndexes int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT (subject_json->>'preview')::boolean FROM jobs WHERE id = '00000000-0000-4000-8000-000000000964'),
		       (SELECT content IS NULL FROM import_uploads WHERE storage_key = 'scenario-preview'),
		       (SELECT count(*) FROM deletion_tombstones WHERE project_id = $1),
		       (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued' AND subject_json->>'sourceView' = 'draft')
	`, projectID).Scan(&previewSubject, &scrubbed, &tombstones, &textIndexes); err != nil {
		t.Fatal(err)
	}
	if !previewSubject || !scrubbed || tombstones != 0 || textIndexes != 0 {
		t.Fatalf("preview subject %v, file scrubbed %v, tombstones %d, draft text indexes %d; want a marked preview that wrote nothing",
			previewSubject, scrubbed, tombstones, textIndexes)
	}

	updated := runImport("00000000-0000-4000-8000-000000000965", "scenario-update", library.ImportModeUpdateBook, first.BookID, bookexport.FormatDOCX, update)
	if got := scenarioCounts(updated); got != wantCounts || updated.Preview != nil {
		t.Fatalf("update counts = %+v, want %+v without a preview", got, wantCounts)
	}
	after := scenarioChapters(t, db, first.BookID)
	words, spaces, renamed, signs, same, removed := after["Глава 1. Утро"], after["Глава 2. Река"], after["Глава 3. Мост"], after["Глава 4. Лагерь"], after["Глава 5. Ночь"], after["Глава 6. Записка"]
	switch {
	case words.id != before["Глава 1. Утро"].id || words.revision != before["Глава 1. Утро"].revision+1:
		t.Fatalf("chapter with one word = %+v, before %+v; want its next revision", words, before["Глава 1. Утро"])
	case spaces != before["Глава 2. Река"]:
		t.Fatalf("chapter with other spaces = %+v, before %+v; want it as it was", spaces, before["Глава 2. Река"])
	case renamed.id != before["Глава 3. Переправа"].id || renamed.revision != before["Глава 3. Переправа"].revision || renamed.deleted:
		t.Fatalf("renamed chapter = %+v, before %+v; want the same chapter and text", renamed, before["Глава 3. Переправа"])
	case signs.id != before["Глава 4. Лагерь"].id || signs.revision != before["Глава 4. Лагерь"].revision+1:
		t.Fatalf("chapter with other signs = %+v, before %+v; want its next revision", signs, before["Глава 4. Лагерь"])
	case same != before["Глава 5. Ночь"]:
		t.Fatalf("unchanged chapter = %+v, before %+v", same, before["Глава 5. Ночь"])
	case removed.id != before["Глава 6. Записка"].id || removed.deleted != removal:
		t.Fatalf("sixth chapter = %+v; want it in «Недавно удалённые» only when the file lacks it", removed)
	}
	var carriedFrom []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT built_from FROM chapter_cards WHERE project_id = $1 AND chapter_id = $2 AND built_from IS NOT NULL
	`, projectID, signs.id).Scan(&carriedFrom); err != nil || len(carriedFrom) != 32 {
		t.Fatalf("carried card of the chapter with other signs = %x, %v", carriedFrom, err)
	}
	var usedImports int
	if err := db.Pool.QueryRow(ctx, `SELECT coalesce(sum(imports), 0) FROM writer_usage_days WHERE user_id = $1`, userID).Scan(&usedImports); err != nil {
		t.Fatal(err)
	}
	if usedImports != 2 {
		t.Fatalf("uploads counted = %d, want 2: the book and its update with the preview (R6.4)", usedImports)
	}

	model.reset()
	built := indexAndCard()
	if built.Built != 1 || model.chapters() != 1 {
		t.Fatalf("cards after the update = %+v, %d chapters read by the model; want one new card", built, model.chapters())
	}
	if queued, err := queueLayer.Execute(ctx, projectID, "draft"); err != nil || queued != removal {
		t.Fatalf("layer queued after the update = %v, %v; want %v: only a removed chapter takes the threads away", queued, err, removal)
	}
	var cardsOfSigns int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chapter_cards WHERE project_id = $1 AND chapter_id = $2`, projectID, signs.id).Scan(&cardsOfSigns); err != nil {
		t.Fatal(err)
	}
	if cardsOfSigns != 1 {
		t.Fatalf("cards of the chapter with other signs = %d, want the carried one only", cardsOfSigns)
	}
}

var scenarioBook = []scenarioChapter{
	{"Глава 1. Утро", []string{"Утро выдалось холодным.", "Отряд из пятнадцати человек вышел к реке на рассвете, и никто не оглянулся на лагерь."}},
	{"Глава 2. Река", []string{"Вода была тёмной — и быстрой.", "Кир молчал до самого брода."}},
	{"Глава 3. Переправа", []string{"Мост качался под ногами.", "Мила шла последней и считала доски."}},
	{"Глава 4. Лагерь", []string{"«Стой», — сказал он.", "Ёлка у костра горела до утра."}},
	{"Глава 5. Ночь", []string{"Ночью пошёл снег."}},
	{"Глава 6. Записка", []string{"Короткая записка, которой в новом файле нет."}},
}

var scenarioUpdate = []scenarioChapter{
	{"Глава 1. Утро", []string{"Утро выдалось  холодным.", "Отряд из тринадцати человек вышел к реке на рассвете, и никто не оглянулся на лагерь."}},
	{"Глава 2. Река", []string{"Вода была тёмной — и быстрой. ", "Кир  молчал до самого брода."}},
	{"Глава 3. Мост", []string{"Мост качался под ногами.", "Мила шла последней и считала доски."}},
	{"Глава 4. Лагерь", []string{"„Стой“, — сказал он…", "Елка у костра горела до утра!"}},
	{"Глава 5. Ночь", []string{"Ночью пошёл снег."}},
}

type scenarioChapter struct {
	title      string
	paragraphs []string
}

func scenarioFile(t *testing.T, format bookexport.Format, chapters []scenarioChapter) []byte {
	t.Helper()
	book := bookexport.Book{
		ID: "75c4aac5-0000-4000-8000-000000000960", Title: "Том 3. Черновик", Author: "Мирон Сватов",
		Date: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Text: func(index int) ([]string, error) { return chapters[index].paragraphs, nil },
	}
	for _, chapter := range chapters {
		book.Chapters = append(book.Chapters, chapter.title)
	}
	var file bytes.Buffer
	if err := bookexport.Write(&file, format, book); err != nil {
		t.Fatal(err)
	}
	return file.Bytes()
}

// parsedScenarioChapters reads a file the way the import Job does: the parser of its format, chapters with text.
func parsedScenarioChapters(t *testing.T, format bookexport.Format, payload []byte) []library.FinalizeImportChapterInput {
	t.Helper()
	var parsed library.BookImport
	var err error
	if format == bookexport.FormatDOCX {
		parsed, err = library.ParseDOCX(bytes.NewReader(payload), int64(len(payload)))
	} else {
		parsed, err = library.ParseFB2(bytes.NewReader(payload))
	}
	if err != nil {
		t.Fatal(err)
	}
	var chapters []library.FinalizeImportChapterInput
	for _, chapter := range parsed.Chapters {
		input := library.FinalizeImportChapterInput{Title: strings.TrimSpace(chapter.Title)}
		for _, paragraph := range chapter.Paragraphs {
			if text := strings.TrimSpace(paragraph); text != "" {
				input.Paragraphs = append(input.Paragraphs, library.ParagraphInput{Kind: library.ParagraphKindParagraph, Order: len(input.Paragraphs) + 1, Markdown: text, Text: &text})
			}
		}
		if len(input.Paragraphs) > 0 {
			chapters = append(chapters, input)
		}
	}
	return chapters
}

func scenarioCounts(finalized library.FinalizedImport) library.FinalizedImport {
	return library.FinalizedImport{
		UnchangedChapters: finalized.UnchangedChapters, ChangedChapters: finalized.ChangedChapters, AddedChapters: finalized.AddedChapters,
		RemovedChapters: finalized.RemovedChapters, PunctuationChapters: finalized.PunctuationChapters, RenamedChapters: finalized.RenamedChapters,
	}
}

func scenarioChapters(t *testing.T, db *storedb.DB, bookID string) map[string]importUpdateChapterRow {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
		SELECT id::text, title, "order", revision, COALESCE(published_revision, 0), deleted_at IS NOT NULL
		FROM chapters WHERE book_id = $1
	`, bookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	byTitle := map[string]importUpdateChapterRow{}
	for rows.Next() {
		var row importUpdateChapterRow
		if err := rows.Scan(&row.id, &row.title, &row.order, &row.revision, &row.publishedRevision, &row.deleted); err != nil {
			t.Fatal(err)
		}
		byTitle[row.title] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return byTitle
}

func scenarioSameChapters(a, b map[string]importUpdateChapterRow) bool {
	if len(a) != len(b) {
		return false
	}
	for title, row := range a {
		if b[title] != row {
			return false
		}
	}
	return true
}

// finishScenarioLayer finishes the layer Job the queue started with the chapters it would read, as a build does.
func finishScenarioLayer(t *testing.T, claims *workerClaimAuthorityForLibraryTest, saga *sagarepo.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()
	decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindSagaLayer})
	if err != nil {
		t.Fatalf("layer claim: %v", err)
	}
	stamps, err := saga.Stamps(ctx, projectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	subject := jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: projectID, SourceView: governance.SourceViewDraft}
	if err := saga.FinishLayer(ctx, decision.Claim, layerservice.LayerOutcome{
		Subject: subject, Status: jobs.StatusSucceeded, Digest: layerdomain.Digest(stamps), Model: "test-model",
		Result: &jobs.SagaLayerResult{Type: jobs.KindSagaLayer, SourceView: governance.SourceViewDraft, Chapters: len(stamps)},
		Layer:  &layerdomain.Layer{Chapters: stamps, Books: []layerdomain.Book{}, Threads: []layerdomain.Thread{}},
	}); err != nil {
		t.Fatal(err)
	}
}

// scenarioCardModel answers the cards Job and counts the chapters it writes a card of.
type scenarioCardModel struct {
	mu    sync.Mutex
	calls int
}

func (model *scenarioCardModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions == memorydomain.NameInstructions {
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	if strings.Contains(input, "КАРТОЧКА:") {
		return `{}`, quota.ModelUsage{Calls: 1}, nil
	}
	model.mu.Lock()
	model.calls++
	model.mu.Unlock()
	return `{"summary": "Пересказ главы"}`, quota.ModelUsage{Calls: 1}, nil
}

func (model *scenarioCardModel) chapters() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls
}

func (model *scenarioCardModel) reset() {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls = 0
}
