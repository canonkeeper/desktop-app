package sagamemory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	memoryOwnerID   = "93000000-0000-4000-8000-000000000001"
	memoryProjectID = "93000000-0000-4000-8000-000000000002"
	memoryBookOne   = "93000000-0000-4000-8000-000000000011"
	memoryBookTwo   = "93000000-0000-4000-8000-000000000012"
)

type seededChapter struct {
	id, book, title string
	order           int
	paragraphs      []string
}

var memoryChapters = []seededChapter{
	{id: "93000000-0000-4000-8000-000000000103", book: memoryBookTwo, title: "Глава 3. Вьюжница", order: 0, paragraphs: []string{"Жрица вьюжница ждала."}},
	{id: "93000000-0000-4000-8000-000000000101", book: memoryBookOne, title: "Глава 1. Начало", order: 0, paragraphs: []string{"Друг по отряду позвал Мирона.", "Зимого́рск был далеко."}},
	{id: "93000000-0000-4000-8000-000000000102", book: memoryBookOne, title: "Глава 2. Дорога", order: 1, paragraphs: []string{"Ёлки и дорога."}},
}

func seedMemoryProject(t *testing.T, db *storedb.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,'memory@example.test','M','hash',$2)", memoryOwnerID, now)
	exec("INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Memory','',$3,$3)", memoryProjectID, memoryOwnerID, now)
	exec("INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'draft',1,1)", memoryProjectID)
	for order, book := range []string{memoryBookOne, memoryBookTwo} {
		title := fmt.Sprintf("Том %d", order+1)
		exec(`INSERT INTO books (id,project_id,title,"order",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'draft',1,$6,$6)`,
			book, memoryProjectID, title, order+1, fmt.Sprint(order+1), now)
		exec("INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,'draft',1,$2,1,$3)",
			memoryProjectID, book, title)
	}
	for _, chapter := range memoryChapters {
		exec(`INSERT INTO chapters (id,project_id,book_id,title,"order",status,revision,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'draft',1,$6,$6)`,
			chapter.id, memoryProjectID, chapter.book, chapter.title, chapter.order, now)
		for position, text := range chapter.paragraphs {
			exec(`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES (gen_random_uuid(),$1,1,'draft','paragraph',$2,$3,$3,3)`,
				chapter.id, position+1, text)
		}
		// A published variant of the same revision must not leak into the draft version.
		exec(`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES (gen_random_uuid(),$1,1,'published','paragraph',1,'чужой текст','чужой текст',2)`, chapter.id)
		generation, err := governance.SourceGenerationDigest(governance.SourceGenerationVector{
			ProjectID: memoryProjectID, ProjectGeneration: 1, BookID: chapter.book, BookGeneration: 1, ChapterID: chapter.id, ChapterGeneration: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,1,$4,$5)",
			memoryProjectID, chapter.book, chapter.id, chapter.title, chapter.order)
		exec("INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,1,$3,$4,$5)",
			memoryProjectID, chapter.id, generation[:], generation[:], now)
	}
}

func TestTextSourceLoadsSelectionInReadingOrder(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory")
	seedMemoryProject(t, db)
	repository := New(db)
	ctx := context.Background()
	selection, err := repository.Selection(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Chapters) != 3 || selection.Fingerprint == "" {
		t.Fatalf("selection = %#v", selection)
	}
	want := []string{"Глава 1. Начало", "Глава 2. Дорога", "Глава 3. Вьюжница"}
	for i, chapter := range selection.Chapters {
		if chapter.Title != want[i] {
			t.Fatalf("chapter %d = %q, want reading order %v", i, chapter.Title, want)
		}
	}
	chapters, err := repository.Chapters(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters[0].Paragraphs) != 2 || chapters[0].Paragraphs[1].Text != "Зимого́рск был далеко." || chapters[0].Paragraphs[0].Text != "Друг по отряду позвал Мирона." {
		t.Fatalf("chapter paragraphs = %#v, want only the draft revision in order", chapters[0].Paragraphs)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE chapter_source_members SET source_revision = 2 WHERE chapter_id = $1", memoryChapters[2].id); err != nil {
		t.Fatal(err)
	}
	changed, err := repository.Selection(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint == selection.Fingerprint || len(changed.Chapters) != 2 {
		t.Fatalf("after a revision without indexed text: %d chapters, fingerprint changed %v; want the chapter excluded",
			len(changed.Chapters), changed.Fingerprint != selection.Fingerprint)
	}
}

// A search call reads the outline and only the rows it needs from PostgreSQL; a text index state
// without search rows makes the saga not ready (specs/2026-10-28-saga-search-in-postgres.md,
// UC2, R2.5).
func TestSagaSearchesTheStoredRows(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_search")
	seedMemoryProject(t, db)
	saga := service.NewSaga(New(db), New(db))
	ctx := context.Background()
	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if outline.Ready() {
		t.Fatal("outline of a version without search rows is ready")
	}
	if _, _, err := saga.Search(ctx, outline, []domain.Query{searchQuery("зимогорск")}, noneShown); !errors.Is(err, domain.ErrSearchNotReady) {
		t.Fatalf("search without rows = %v, want ErrSearchNotReady", err)
	}

	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	outline, err = saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil || !outline.Ready() || outline.Version.Chapters[0].ParagraphCount != 2 || outline.Version.Chapters[0].Paragraphs != nil {
		t.Fatalf("outline = %+v, %v; want counts without text", outline, err)
	}
	shown := func(_ domain.Query, result domain.Result) []domain.Hit { return result.Hits }
	index, results, err := saga.Search(ctx, outline, []domain.Query{searchQuery("зимогорск"), searchQuery("отряду")}, shown)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Hits) != 1 || len(results[1].Hits) != 1 {
		t.Fatalf("hits = %d, %d; want the stressed word and the case form", len(results[0].Hits), len(results[1].Hits))
	}
	if chapter, paragraph := index.Locate(results[0].Hits[0].Para); chapter.ID != memoryChapters[1].id || paragraph.N != 2 || paragraph.Text != "Зимого́рск был далеко." {
		t.Fatalf("hit = %s ¶%d %q", chapter.ID, paragraph.N, paragraph.Text)
	}
	if _, results, err := saga.Search(ctx, outline, []domain.Query{{Text: "могор", Mode: domain.ModeSubstring, To: -1}}, noneShown); err != nil || len(results[0].Hits) != 1 {
		t.Fatalf("substring = %+v, %v", results, err)
	}
}

// The cap counts the stored postings of a stem over the given texts; a stem over it comes back with no row.
func TestRareStemWordsLeavesOutStemsOverTheCap(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_rare_stems")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	repository := New(db)
	ctx := context.Background()
	outline, err := service.NewSaga(repository, repository).Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	stems := []string{"зимогорск", "отряд"}
	all, err := repository.StemWords(ctx, memoryProjectID, outline.TextIDs(), stems)
	if err != nil || len(all) != len(stems) {
		t.Fatalf("StemWords() = %+v, %v", all, err)
	}
	for _, row := range all {
		size := int64(len(row.Postings))
		fits, err := repository.RareStemWords(ctx, memoryProjectID, outline.TextIDs(), []string{row.Stem}, size)
		if err != nil || len(fits) != 1 || !bytes.Equal(fits[0].Postings, row.Postings) {
			t.Fatalf("RareStemWords(%s) at its size = %+v, %v", row.Stem, fits, err)
		}
		over, err := repository.RareStemWords(ctx, memoryProjectID, outline.TextIDs(), []string{row.Stem}, size-1)
		if err != nil || len(over) != 0 {
			t.Fatalf("RareStemWords(%s) under its size = %+v, %v", row.Stem, over, err)
		}
	}
}

func noneShown(domain.Query, domain.Result) []domain.Hit { return nil }

// countingSource counts the paragraph reads of a saga.
type countingSource struct {
	*Repository
	paragraphs int
}

func (source *countingSource) Paragraphs(ctx context.Context, projectID, view string, keys []service.ParagraphKey) ([]service.ParagraphText, error) {
	source.paragraphs++
	return source.Repository.Paragraphs(ctx, projectID, view, keys)
}

func searchQuery(text string) domain.Query { return domain.Query{Text: text, To: -1} }

func TestFindChapterResolvesKeysWithoutLoadingText(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_keys")
	seedMemoryProject(t, db)
	counting := &countingSource{Repository: New(db)}
	saga := service.NewSaga(counting, New(db))
	chapter, ok, err := saga.FindChapter(context.Background(), memoryProjectID, "draft", "гл. 3")
	if err != nil || !ok || chapter.ID != memoryChapters[0].id || chapter.Key != "3" || chapter.BookID != memoryBookTwo {
		t.Fatalf("FindChapter(гл. 3) = %+v, %v, %v", chapter, ok, err)
	}
	if _, ok, err := saga.FindChapter(context.Background(), memoryProjectID, "draft", "99"); ok || err != nil {
		t.Fatalf("FindChapter(99) = %v, %v; want not found", ok, err)
	}
	if counting.paragraphs != 0 {
		t.Fatalf("key resolution loaded chapter text %d times", counting.paragraphs)
	}
}
