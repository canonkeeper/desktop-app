package canonstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"

	"github.com/jackc/pgx/v5"
)

// A deleted chapter takes its derived text data along at the fence: the text index state, the
// card, the saga search rows (specs/2026-10-28-saga-search-in-postgres.md, R5.2) and the project's
// name registry (specs/2026-11-01-index-foundation.md, R5.3).
func TestChapterDeletionFenceScrubsCardsAndSearchRows(t *testing.T) {
	fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
	ctx := context.Background()
	hash := make([]byte, 32)
	hash[0] = 7
	// The canon store fixture has no text index or cards path (they live in store/indexing and in
	// the cards Job); the derived rows of the chapter the fence must remove are written directly.
	for _, statement := range []string{
		`INSERT INTO chapter_cards (project_id, chapter_id, content_hash, card_json, model, version)
		 VALUES ($1, $2, $3, '{"summary":"s"}', 'model', 2)`,
		`WITH text AS (
		   INSERT INTO chapter_search_texts (project_id, chapter_id, content_hash, paragraph_orders, paragraph_tokens, text_runes)
		   VALUES ($1, $2, $3, '{1}', '{1}', 4) RETURNING id, project_id
		 )
		 INSERT INTO chapter_search_words (text_id, project_id, word, stem, postings)
		 SELECT id, project_id, 'слово', 'слов', '\x010100'::bytea FROM text`,
	} {
		if _, err := fixture.db.Pool.Exec(ctx, statement, fixture.projectID, fixture.chapterID, hash); err != nil {
			t.Fatal(err)
		}
	}
	insertSagaNamesForTest(t, fixture)
	insertSagaThreadsForTest(t, fixture)

	fenceChapterDeletionForTest(t, fixture)

	var cards, texts, words int
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM chapter_cards WHERE project_id = $1),
		       (SELECT count(*) FROM chapter_search_texts WHERE project_id = $1),
		       (SELECT count(*) FROM chapter_search_words WHERE project_id = $1)`, fixture.projectID).Scan(&cards, &texts, &words); err != nil {
		t.Fatal(err)
	}
	if cards != 0 || texts != 0 || words != 0 {
		t.Fatalf("after the fence: cards %d, search texts %d, search words %d; want none", cards, texts, words)
	}
	if names := countSagaNamesForTest(t, fixture); names != 0 {
		t.Fatalf("after the fence: %d name registries; want none", names)
	}
	if layers := countSagaThreadsForTest(t, fixture); layers != 0 {
		t.Fatalf("after the fence: %d thread layers; want none", layers)
	}
}

// R5.3: any selection removes the registry of the project in the fence transaction: a registry may
// hold names of the deleted book or chapter whichever it was.
func TestScrubRemovesTheNameRegistryForEverySelection(t *testing.T) {
	for _, kind := range []governance.DeletionSelectionKind{
		governance.DeletionSelectionProject, governance.DeletionSelectionBook, governance.DeletionSelectionChapter,
	} {
		fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
		ctx := context.Background()
		insertSagaNamesForTest(t, fixture)
		insertSagaThreadsForTest(t, fixture)
		vector := governance.SourceGenerationVector{ProjectID: fixture.projectID, ProjectGeneration: 1}
		if kind != governance.DeletionSelectionProject {
			vector.BookID, vector.BookGeneration = fixture.bookID, 1
		}
		if kind == governance.DeletionSelectionChapter {
			vector.ChapterID, vector.ChapterGeneration = fixture.chapterID, 1
		}
		if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
			return fixture.db.ScrubCanonicalDerivedSourceInTx(ctx, tx, kind, vector, time.Now().UTC())
		}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if names := countSagaNamesForTest(t, fixture); names != 0 {
			t.Fatalf("%s: %d name registries after the fence; want none", kind, names)
		}
		if layers := countSagaThreadsForTest(t, fixture); layers != 0 {
			t.Fatalf("%s: %d thread layers after the fence; want none", kind, layers)
		}
	}
}

func insertSagaNamesForTest(t *testing.T, fixture governedChapterFixtureForTest) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		INSERT INTO saga_names (project_id, digest, names_json, model) VALUES ($1, $2, '{"persons":[]}', 'model')
	`, fixture.projectID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
}

func countSagaNamesForTest(t *testing.T, fixture governedChapterFixtureForTest) int {
	t.Helper()
	var names int
	if err := fixture.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM saga_names WHERE project_id = $1`, fixture.projectID).Scan(&names); err != nil {
		t.Fatal(err)
	}
	return names
}

func insertSagaThreadsForTest(t *testing.T, fixture governedChapterFixtureForTest) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		INSERT INTO saga_threads (project_id, digest, threads_json, checkpoint_json, model)
		VALUES ($1, $2, '[{"thread":"нить"}]', '{"digest":"d"}', 'model')
	`, fixture.projectID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
}

func countSagaThreadsForTest(t *testing.T, fixture governedChapterFixtureForTest) int {
	t.Helper()
	var layers int
	if err := fixture.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM saga_threads WHERE project_id = $1`, fixture.projectID).Scan(&layers); err != nil {
		t.Fatal(err)
	}
	return layers
}
