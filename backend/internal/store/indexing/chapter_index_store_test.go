package indexingstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"

	"github.com/jackc/pgx/v5"
)

func TestStoreChapterReplaceWritesExactlyOneSearchRow(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 7300, governance.SourceViewDraft)
	content := strings.Repeat("Текст целой главы. ", 500)
	if _, err := fixture.db.Pool.Exec(ctx, `
		UPDATE chapter_paragraphs SET plain_text=$2, markdown=$2 WHERE chapter_id=$1 AND variant='draft'
	`, fixture.chapterID, content); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)

	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}

	var rows int
	var storedContent string
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2),
		  (SELECT content FROM chapter_text_search_rows WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2)
	`, fixture.projectID, fixture.chapterID).Scan(&rows, &storedContent); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || storedContent != snapshot.Paragraphs[0].Content {
		t.Fatalf("rows/content = %d/%q", rows, storedContent)
	}
}

func TestStoreChapterSearchPreflightRollsBackOversize(t *testing.T) {
	t.Run("model budget", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7301, governance.SourceViewDraft)
		content := "a" + strings.Repeat(" ", indexing.WholeChapterTransferMaxBytes)
		if _, err := fixture.db.Pool.Exec(ctx, `
			UPDATE chapter_paragraphs SET plain_text=$2, markdown=$2 WHERE chapter_id=$1 AND variant='draft'
		`, fixture.chapterID, content); err != nil {
			t.Fatal(err)
		}
		snapshot := fixture.snapshot(t)
		_, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		})
		if !errors.Is(err, ErrTextIndexChapterOversize) {
			t.Fatalf("oversize error = %v", err)
		}
		assertNoCanonicalIndexRows(t, fixture)
	})

	t.Run("PostgreSQL tsvector", func(t *testing.T) {
		tracer := &chapterTransactionTracer{}
		fixture := newStoreFixture(t, 7302, governance.SourceViewDraft, tracer)
		var content strings.Builder
		for index := 0; index < 120_000; index++ {
			fmt.Fprintf(&content, "%06d ", index)
		}
		if _, err := fixture.db.Pool.Exec(context.Background(), `
			UPDATE chapter_paragraphs SET plain_text=$2, markdown=$2 WHERE chapter_id=$1 AND variant='draft'
		`, fixture.chapterID, content.String()); err != nil {
			t.Fatal(err)
		}
		snapshot := fixture.snapshot(t)
		_, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		})
		if !errors.Is(err, ErrTextIndexChapterOversize) {
			t.Fatalf("PostgreSQL oversize error = %v", err)
		}
		if tracer.started.Load() {
			t.Fatal("PostgreSQL oversize was detected only after starting the write transaction")
		}
		assertNoCanonicalIndexRows(t, fixture)
	})
}

type chapterTransactionTracer struct {
	started atomic.Bool
}

func (tracer *chapterTransactionTracer) TraceQueryStart(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(strings.TrimSpace(data.SQL), "rollback") {
		tracer.started.Store(true)
	}
	return context.Background()
}

func (*chapterTransactionTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
