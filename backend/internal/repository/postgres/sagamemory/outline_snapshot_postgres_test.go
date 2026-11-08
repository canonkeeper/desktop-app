package sagamemory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/sagamemory"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// afterSelection runs a step once, right after the chapter selection of a text version is read.
type afterSelection struct {
	once sync.Once
	step func()
}

type selectionRead struct{}

func (hook *afterSelection) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "-- name: ListCanonicalTextSelectionRows ") {
		return context.WithValue(ctx, selectionRead{}, true)
	}
	return ctx
}

func (hook *afterSelection) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(selectionRead{}) == true && data.Err == nil && hook.step != nil {
		hook.once.Do(hook.step)
	}
}

// deleteChapterForTest commits what the fence of a chapter deletion does to the saga memory: the
// chapter is deleted and its derived text data is scrubbed by the fence's own scrub. The library
// deletion flow and its governance tombstone are not reachable from this package, so the chapter
// row is marked deleted directly with its triggers off.
func deleteChapterForTest(t *testing.T, db *storedb.DB, chapter seededChapter) {
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = replica"); err != nil {
		t.Error(err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE chapters SET deleted_at = $2::timestamptz, governance_retention_started_at = $2::timestamptz,
		purge_after = $2::timestamptz + interval '7 days', purge_deadline = $2::timestamptz + interval '8 days' WHERE id = $1::uuid`, chapter.id, now); err != nil {
		t.Error(err)
		return
	}
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = origin"); err != nil {
		t.Error(err)
		return
	}
	vector := governance.SourceGenerationVector{
		ProjectID: memoryProjectID, ProjectGeneration: 1, BookID: chapter.book, BookGeneration: 1, ChapterID: chapter.id, ChapterGeneration: 1,
	}
	if err := canonstore.New(db).ScrubCanonicalDerivedSourceInTx(ctx, tx, governance.DeletionSelectionChapter, vector, now); err != nil {
		t.Error(err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Error(err)
	}
}

// A chapter deleted while a call reads its text version: the outline is one snapshot of the
// chapter selection and their search texts, so the deletion between the two reads does not make
// the saga «not ready»; the words of the deleted chapter are gone with it, so a search finds
// nothing there, and a paragraph of it the call reads is gone too: the call fails with
// ErrTextChanged, which the agent retries, instead of answering without its text
// (specs/2026-10-28-saga-search-in-postgres.md, UC2, R2.5).
func TestACallReadsOneSnapshotAndRetriesAChapterDeletedDuringIt(t *testing.T) {
	hook := &afterSelection{}
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_saga_memory_snapshot", hook)
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	deleted := memoryChapters[0]
	hook.step = func() { deleteChapterForTest(t, db, deleted) }
	saga := service.NewSaga(New(db), New(db))
	ctx := context.Background()

	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if !outline.Ready() || len(outline.Version.Chapters) != len(memoryChapters) {
		t.Fatalf("outline of %d chapters is ready %v; want the snapshot before the deletion, ready", len(outline.Version.Chapters), outline.Ready())
	}
	shown := func(_ domain.Query, result domain.Result) []domain.Hit { return result.Hits }
	if _, results, err := saga.Search(ctx, outline, []domain.Query{searchQuery("вьюжница")}, shown); err != nil || len(results[0].Hits) != 0 {
		t.Fatalf("search for a word only the deleted chapter has = %+v, %v; want no hits", results, err)
	}
	at := -1
	for i, chapter := range outline.Version.Chapters {
		if chapter.ID == deleted.id {
			at = i
		}
	}
	if err := saga.Paragraphs(ctx, outline, map[int][]int{at: {0}}); !errors.Is(err, domain.ErrTextChanged) || outline.Version.Chapters[at].Paragraphs != nil {
		t.Fatalf("reading the deleted chapter = %v, paragraphs %v; want ErrTextChanged and none", err, outline.Version.Chapters[at].Paragraphs)
	}

	outline, err = saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil || !outline.Ready() || len(outline.Version.Chapters) != len(memoryChapters)-1 {
		t.Fatalf("outline after the deletion = %d chapters, ready %v, %v; want the rest, ready", len(outline.Version.Chapters), outline.Ready(), err)
	}
}
