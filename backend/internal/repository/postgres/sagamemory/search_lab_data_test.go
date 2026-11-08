package sagamemory

// The saga search lab (specs/2026-10-28-saga-search-in-postgres.md, UC6) compares the product
// reader of the saga search rows with the frozen in-memory index (search_lab_oracle_test.go) on
// real sagas. The recorded tool calls, and the sagas unless the lab reads a database, come from
// CANONKEEPER_SAGA_SEARCH_LAB, a directory outside the repository (an export of TEST made by
// testdata/saga_search_lab_export.sql); without it the lab is skipped. Nothing from the sagas is
// logged: only counts, sizes and timings.

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type labChapterRow struct {
	project, book, chapter string
	revision               int64
	title                  string
	order                  int
	contentHash            string
}

type labBookRow struct {
	project, book string
	order         int
	title         string
}

type labCardRow struct {
	project, chapter, contentHash, card, model string
	version                                    int
}

type labSearchCall struct {
	Project string   `json:"project"`
	Queries []string `json:"queries"`
	Match   string   `json:"match"`
	Order   string   `json:"order"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Books   []int    `json:"books"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

type labToolCall struct {
	Project string         `json:"project"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
}

type labData struct {
	books      []labBookRow
	chapters   []labChapterRow
	paragraphs map[string][]string // chapter id -> texts in order
	cards      []labCardRow
	search     []labSearchCall
	tools      []labToolCall
}

func labDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("CANONKEEPER_SAGA_SEARCH_LAB")
	if dir == "" {
		t.Skip("CANONKEEPER_SAGA_SEARCH_LAB names the exported sagas of the search lab")
	}
	return dir
}

func readLabCSV(t *testing.T, dir, name string, visit func([]string)) {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return // a lab over a database needs only the recorded calls
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		visit(record)
	}
}

func loadLabData(t *testing.T, dir string) labData {
	t.Helper()
	data := labData{paragraphs: map[string][]string{}}
	atoi := func(value string) int {
		n, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	readLabCSV(t, dir, "books.csv", func(r []string) {
		data.books = append(data.books, labBookRow{project: r[0], book: r[1], order: atoi(r[2]), title: r[3]})
	})
	readLabCSV(t, dir, "chapters.csv", func(r []string) {
		data.chapters = append(data.chapters, labChapterRow{project: r[0], book: r[1], chapter: r[2], revision: int64(atoi(r[3])), title: r[4], order: atoi(r[5]), contentHash: r[6]})
	})
	readLabCSV(t, dir, "paragraphs.csv", func(r []string) {
		data.paragraphs[r[0]] = append(data.paragraphs[r[0]], r[2])
	})
	readLabCSV(t, dir, "cards.csv", func(r []string) {
		data.cards = append(data.cards, labCardRow{project: r[0], chapter: r[1], contentHash: r[2], card: r[3], model: r[4], version: atoi(r[5])})
	})
	for name, target := range map[string]any{"search_calls.json": &data.search, "tool_calls.json": &data.tools} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

// labProjects lists the exported projects, the largest first.
func (data labData) labProjects() []string {
	count := map[string]int{}
	for _, chapter := range data.chapters {
		count[chapter.project]++
	}
	projects := make([]string, 0, len(count))
	for project := range count {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return count[projects[i]] > count[projects[j]] })
	return projects
}

// seedLabProject writes one exported saga as the draft text version of a project: the same rows
// the import and the text index Job leave, plus the chapter cards.
func seedLabProject(t *testing.T, db *storedb.DB, data labData, project string, owner int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	user := fmt.Sprintf("94000000-0000-4000-8000-%012d", owner)
	exec("INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,'Lab','hash',$3)", user, fmt.Sprintf("lab%d@example.test", owner), now)
	exec("INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Lab','',$3,$3)", project, user, now)
	exec("INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'draft',1,1)", project)
	for _, book := range data.books {
		if book.project != project {
			continue
		}
		exec(`INSERT INTO books (id,project_id,title,"order",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'draft',1,$6,$6)`,
			book.book, project, book.title, book.order, strconv.Itoa(book.order), now)
		exec("INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,'draft',1,$2,1,$3)",
			project, book.book, book.title)
	}
	var paragraphs [][]any
	for _, chapter := range data.chapters {
		if chapter.project != project {
			continue
		}
		exec(`INSERT INTO chapters (id,project_id,book_id,title,"order",status,revision,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'draft',$6,$7,$7)`,
			chapter.chapter, project, chapter.book, chapter.title, chapter.order, chapter.revision, now)
		exec("INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,$4,$5,$6)",
			project, chapter.book, chapter.chapter, chapter.revision, chapter.title, chapter.order)
		generation, err := governance.SourceGenerationDigest(governance.SourceGenerationVector{
			ProjectID: project, ProjectGeneration: 1, BookID: chapter.book, BookGeneration: 1, ChapterID: chapter.chapter, ChapterGeneration: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		hash, err := hex.DecodeString(chapter.contentHash)
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at) VALUES ($1,'draft',$2,$3,$4,$5,$6)",
			project, chapter.chapter, chapter.revision, generation[:], hash, now)
		for order, text := range data.paragraphs[chapter.chapter] {
			paragraphs = append(paragraphs, []any{chapter.chapter, chapter.revision, "draft", "paragraph", order + 1, text, text, 0})
		}
	}
	copyRows(t, db, "chapter_paragraphs", []string{"chapter_id", "revision", "variant", "kind", "order", "markdown", "plain_text", "word_count"}, paragraphs, "id")
	var cards [][]any
	for _, card := range data.cards {
		if card.project != project {
			continue
		}
		hash, err := hex.DecodeString(card.contentHash)
		if err != nil {
			t.Fatal(err)
		}
		cards = append(cards, []any{project, card.chapter, hash, card.card, card.model, card.version})
	}
	copyRows(t, db, "chapter_cards", []string{"project_id", "chapter_id", "content_hash", "card_json", "model", "version"}, cards, "")
}

// copyRows copies rows into a table; generatedID names a uuid key column filled by the database.
func copyRows(t *testing.T, db *storedb.DB, table string, columns []string, rows [][]any, generatedID string) {
	t.Helper()
	ctx := context.Background()
	if generatedID != "" {
		if _, err := db.Pool.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT gen_random_uuid()", table, generatedID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows)); err != nil {
		t.Fatal(table, err)
	}
}

// buildLabSearchRows writes the search rows of every chapter text of the project the way the
// text index Job does (UC1).
func buildLabSearchRows(t *testing.T, db *storedb.DB, project string) (time.Duration, int) {
	t.Helper()
	started := time.Now()
	testutil.SeedSagaSearchRows(t, db, project)
	var words int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM chapter_search_words WHERE project_id = $1`, project).Scan(&words); err != nil {
		t.Fatal(err)
	}
	return time.Since(started), words
}

func labCardKeys(selection service.Selection) []service.CardKey {
	keys := make([]service.CardKey, len(selection.Chapters))
	for i, chapter := range selection.Chapters {
		keys[i] = service.CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	return keys
}
