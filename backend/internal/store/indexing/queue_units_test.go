package indexingstore

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

type queuePauseKey struct{}

// queuePause stops the transaction whose context carries queuePauseKey right before the first query of the queue of a
// Job of the reading (the share lock of its project in store/jobs.upsertAutomaticIntent): the end of a text index holds
// the share locks of the catalog of its saga there and has not asked for its queue yet.
type queuePause struct {
	once    sync.Once
	paused  chan struct{}
	release chan struct{}
}

func (p *queuePause) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if ctx.Value(queuePauseKey{}) != nil && strings.Contains(data.SQL, "FROM projects") &&
		strings.Contains(data.SQL, "WHERE id = $1 AND deleted_at IS NULL") && strings.Contains(data.SQL, "FOR SHARE") {
		p.once.Do(func() {
			close(p.paused)
			<-p.release
		})
	}
	return ctx
}

func (p *queuePause) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// specs/2026-11-15-desk-reading-queue.md RQ1.3: the queues of the cards Job go one after another by the unit one waiting Job
// lives by — the saga and its text version — not by the whole saga. An import queues the text indexes of both versions,
// and they end at once: the end of the published text holds the books of the saga for share and asks to queue its cards
// Job, the end of the draft has queued its own and makes its books ready. Keyed by the saga alone, the published end
// waited for the draft one while the draft one waited for the books — deadlock detected (40P01), the draft index went to a
// retry; keyed by the version, the two queues do not wait for each other, and both ends succeed, as before the slice.
func TestTheEndsOfTheTextIndexesOfBothVersionsAtOnceBothSucceed(t *testing.T) {
	ctx := context.Background()
	pause := &queuePause{paused: make(chan struct{}), release: make(chan struct{})}
	draft := newStoreFixture(t, 8600, governance.SourceViewDraft, pause)
	published := draft
	published.view = governance.SourceViewPublished
	published.claim = addCanonicalIndexClaimForTest(t, published, 21)
	indexCurrentCanonTextChaptersForTest(t, ctx, draft)
	indexCurrentCanonTextChaptersForTest(t, ctx, published)
	// The book of the import is not ready until the end of the index of its draft.
	if _, err := draft.db.Pool.Exec(ctx, `UPDATE books SET status = 'draft' WHERE id = $1`, draft.bookID); err != nil {
		t.Fatal(err)
	}

	publishedDone := make(chan error, 1)
	go func() {
		publishedDone <- published.store.CompleteCanonTextIndexForClaim(context.WithValue(ctx, queuePauseKey{}, true), published.claim, canonTextSubjectForStoreTest(published))
	}()
	select {
	case <-pause.paused:
	case err := <-publishedDone:
		t.Fatalf("the end of the published index came before its queue: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the end of the published index did not reach its queue")
	}
	draftDone := make(chan error, 1)
	go func() {
		draftDone <- draft.store.CompleteCanonTextIndexForClaim(ctx, draft.claim, canonTextSubjectForStoreTest(draft))
	}()
	// The end of the draft has queued its cards Job and waits for the books the published end holds for share.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waits bool
		if err := draft.db.Pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%UPDATE books AS book%')
		`).Scan(&waits); err != nil {
			t.Fatal(err)
		}
		if waits {
			break
		}
		select {
		case err := <-draftDone:
			t.Fatalf("the end of the draft index did not wait for the books of the published end: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the end of the draft index did not reach the books")
		}
	}
	close(pause.release)
	var publishedErr, draftErr error
	for _, done := range []struct {
		name string
		ch   chan error
		err  *error
	}{{"published", publishedDone, &publishedErr}, {"draft", draftDone, &draftErr}} {
		select {
		case *done.err = <-done.ch:
		case <-time.After(30 * time.Second):
			t.Fatalf("the end of the %s index did not come", done.name)
		}
	}
	if publishedErr != nil || draftErr != nil {
		t.Fatalf("the ends of the text indexes of both versions at once: published = %v, draft = %v; want both to succeed", publishedErr, draftErr)
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if got := countStoreRowsForTest(t, draft.db, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND subject_json->>'sourceView' = '`+string(view)+`'`, draft.projectID); got != 1 {
			t.Fatalf("%d cards Jobs of the %s text wait after both ends; want one", got, view)
		}
	}
	if got := countStoreRowsForTest(t, draft.db, `SELECT count(*) FROM books WHERE project_id = $1 AND status = 'ready'`, draft.projectID); got != 1 {
		t.Fatalf("%d books are ready after the end of the draft index; want one", got)
	}
}

// specs/2026-11-15-desk-reading-queue.md RQ1.1: the sample of three chapters lives by its own unit — one live sample of a
// saga — so it waits only for another sample, not for the queue of the cards Job of the published text, which it does not
// take and which does not take it (CN2.2).
func TestTheSampleDoesNotWaitForTheQueueOfTheCards(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 8700, governance.SourceViewPublished)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	id, err := storedb.ParseUUID(fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := fixture.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cards.Rollback(ctx) }()
	var cardsPID uint32
	if err := cards.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&cardsPID); err != nil {
		t.Fatal(err)
	}
	generation, err := lockCanonTextTerminalProject(ctx, cards, id)
	if err != nil {
		t.Fatal(err)
	}
	source, err := projectJobSource(fixture.projectID, generation, governance.SourceViewPublished)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobstore.New(fixture.db).UpsertChapterCardsIntent(ctx, cards, jobs.ChapterCardsIntentInput{
		Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: fixture.projectID, SourceView: governance.SourceViewPublished},
		Source:  source,
	}); err != nil {
		t.Fatal(err)
	}

	sample := make(chan error, 1)
	go func() {
		sample <- pgx.BeginFunc(ctx, fixture.db.Pool, func(tx pgx.Tx) error {
			_, err := fixture.store.QueueCardsSampleInTx(ctx, tx, fixture.projectID, jobs.CardsSample{
				Host: "api.example.test", Chapters: []jobs.SampleChapter{{ChapterID: fixture.chapterID, ContentHash: "hash"}},
			})
			return err
		})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		select {
		case err := <-sample:
			if err != nil {
				t.Fatalf("the sample beside the queue of the cards = %v", err)
			}
			if err := cards.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if got := countStoreRowsForTest(t, fixture.db, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued'`, fixture.projectID); got != 2 {
				t.Fatalf("%d cards Jobs wait after the sample and the queue of the cards; want two", got)
			}
			return
		default:
		}
		var blocked bool
		if err := fixture.db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::integer = ANY(pg_blocking_pids(pid)))`, int32(cardsPID)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			t.Fatal("the sample waits for the queue of the cards Job of the published text; want it to wait only for another sample")
		}
		if time.Now().After(deadline) {
			t.Fatal("the sample neither answered nor waited")
		}
	}
}
