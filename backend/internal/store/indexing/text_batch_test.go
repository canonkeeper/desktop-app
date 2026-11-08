package indexingstore

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"canonkeeper/backend/internal/governance"

	"github.com/jackc/pgx/v5"
)

type canonTextSubmissionCounter struct {
	queries atomic.Int64
	batches atomic.Int64
}

func (counter *canonTextSubmissionCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	counter.queries.Add(1)
	return ctx
}

func (*canonTextSubmissionCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (counter *canonTextSubmissionCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	counter.batches.Add(1)
	return ctx
}

func (*canonTextSubmissionCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {
}

func (*canonTextSubmissionCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (counter *canonTextSubmissionCounter) reset() {
	counter.queries.Store(0)
	counter.batches.Store(0)
}

func TestReplaceCanonTextChapterWritesOneRowWithoutBatches(t *testing.T) {
	counter := &canonTextSubmissionCounter{}
	fixture := newStoreFixture(t, 7950, governance.SourceViewDraft, counter)
	for index := 1; index < 12; index++ {
		paragraphID := indexingStoreUUIDForTest(7960 + index)
		if _, err := fixture.db.Pool.Exec(context.Background(), `
			INSERT INTO chapter_paragraphs (
			  id, chapter_id, revision, variant, kind, "order",
			  markdown, plain_text, word_count, created_at, updated_at
			) VALUES ($1, $2, 1, 'draft', 'paragraph', $3, $4, $4, 2, $5, $5)
		`, paragraphID, fixture.chapterID, index+1, fmt.Sprintf("dragon %d", index+1), fixture.now); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := fixture.snapshot(t)

	counter.reset()
	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	queries, batches := counter.queries.Load(), counter.batches.Load()
	t.Logf("database submissions: %d queries + %d batches", queries, batches)
	if submissions := queries + batches; submissions > 18 {
		t.Fatalf("database submissions = %d (%d queries + %d batches), want at most 18", submissions, queries, batches)
	}
	if batches != 0 {
		t.Fatalf("database batches = %d, want 0", batches)
	}
	assertCanonicalIndexRows(t, fixture, fixture.view, fixture.chapterID, 1)
}

func TestLoadCanonTextChapterSnapshotUsesSingleSubmission(t *testing.T) {
	counter := &canonTextSubmissionCounter{}
	fixture := newStoreFixture(t, 7975, governance.SourceViewDraft, counter)
	snapshot := fixture.snapshot(t)
	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}

	counter.reset()
	current, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(
		context.Background(), fixture.claim, snapshot.CanonTextChapterDescriptor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Unchanged {
		t.Fatal("matching chapter state was not returned with the snapshot")
	}
	if queries, batches := counter.queries.Load(), counter.batches.Load(); queries != 1 || batches != 0 {
		t.Fatalf("database submissions = %d queries + %d batches, want 1 query", queries, batches)
	}
}
