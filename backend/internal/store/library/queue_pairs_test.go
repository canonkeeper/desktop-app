package librarystore

// The lock of the queue of the Jobs of the reading (specs/2026-11-15-desk-reading-queue.md RQ1, «Блокировка постановки»)
// makes no deadlock with its neighbours: the queues of the Jobs of the reading beside the ends of the text indexes of both
// versions of three volumes of one import queue, the claim of the cards, the end of a cards Job, the cancel and the deletion
// of a chapter — every participant through the function of its owner. A pause stops one holder of the lock of a unit right
// after it took the lock (the first query after it: the expiry of the Jobs without access), the others pile up behind it;
// released, everyone must succeed — no deadlock detected, no refusal.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type pairTagKey struct{}

// pairTag names one participant: the backend it runs on, and where it stops.
type pairTag struct {
	name    string
	pid     atomic.Uint32
	match   func(sql string, args []any) bool
	paused  chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

// newPairTag registers the release of its pause with the test, so a failing test does not leave a held backend behind.
func newPairTag(t *testing.T, name string, match func(string, []any) bool) *pairTag {
	tag := &pairTag{name: name, match: match, paused: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(tag.free)
	return tag
}

func (tag *pairTag) free() { tag.freed.Do(func() { close(tag.release) }) }

type pairTracer struct{}

func (pairTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	tag, ok := ctx.Value(pairTagKey{}).(*pairTag)
	if !ok {
		return ctx
	}
	tag.pid.Store(conn.PgConn().PID())
	if tag.match != nil && tag.match(data.SQL, data.Args) {
		tag.once.Do(func() {
			close(tag.paused)
			<-tag.release
		})
	}
	return ctx
}

func (pairTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// afterQueueLock is the first query of store/jobs.upsertAutomaticIntent after its advisory lock: the expiry of the Jobs of
// the kind and version without access.
func afterQueueLock(kind jobs.Kind, view governance.SourceView) func(string, []any) bool {
	return func(sql string, args []any) bool {
		if !strings.Contains(sql, "FOR UPDATE OF job, owner") || len(args) != 3 {
			return false
		}
		gotView, _ := args[1].(string)
		gotKind, _ := args[2].(string)
		return gotKind == string(kind) && gotView == string(view)
	}
}

func pairRun(tag *pairTag, f func(context.Context) error) chan error {
	done := make(chan error, 1)
	go func() { done <- f(context.WithValue(context.Background(), pairTagKey{}, tag)) }()
	return done
}

func pairPaused(t *testing.T, tag *pairTag, done chan error) {
	t.Helper()
	select {
	case <-tag.paused:
	case err := <-done:
		t.Fatalf("%s ended before its pause: %v", tag.name, err)
	case <-time.After(20 * time.Second):
		t.Fatalf("%s did not reach its pause", tag.name)
	}
}

// pairWaits waits until the backend of the participant waits for a lock: advisory — for the lock of a queue, else for a
// row (the books, the project).
func pairWaits(t *testing.T, db *storedb.DB, tag *pairTag, advisory bool, done chan error) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if pid := tag.pid.Load(); pid != 0 {
			var waitType, waitEvent pgtype.Text
			if err := db.Pool.QueryRow(context.Background(), `SELECT wait_event_type, wait_event FROM pg_stat_activity WHERE pid = $1`, int32(pid)).Scan(&waitType, &waitEvent); err == nil {
				if waitType.String == "Lock" && (waitEvent.String == "advisory") == advisory {
					return
				}
				if waitType.String == "Lock" && waitEvent.String == "advisory" && !advisory {
					t.Fatalf("%s waits for the lock of a queue; want it to wait for a row", tag.name)
				}
			}
		}
		select {
		case err := <-done:
			t.Fatalf("%s ended before it waited (advisory %v): %v", tag.name, advisory, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not wait for a lock (advisory %v)", tag.name, advisory)
		}
	}
}

func pairEnded(t *testing.T, name string, done chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("%s did not end within %s", name, within)
		return nil
	}
}

func newQueuePairsFixture(t *testing.T, suffix string) (*storedb.DB, string, string, auth.SessionWriteProof) {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store", pairTracer{})
	ctx := context.Background()
	userID := fmt.Sprintf("00000000-0000-4000-8000-000000000%03s", suffix)
	projectID := fmt.Sprintf("00000000-0000-4000-8000-000000001%03s", suffix)
	membershipID := fmt.Sprintf("00000000-0000-4000-8000-000000002%03s", suffix)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, userID, "review-queue-"+suffix+"@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Queue Pairs Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
	proof := auth.SessionWriteProof{
		SessionHash:    fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":session"))),
		CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":csrf"))),
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, clock_timestamp() + interval '1 hour', clock_timestamp()
	`, userID, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatal(err)
	}
	return db, projectID, userID, proof
}

type pairIndex struct {
	claim   jobs.WorkerClaim
	subject jobs.CanonTextIndexSubject
}

func (index pairIndex) end(indexing *indexingstore.Store) func(context.Context) error {
	return func(ctx context.Context) error {
		return indexing.CompleteCanonTextIndexForClaim(ctx, index.claim, index.subject)
	}
}

func pairQueue(indexing *indexingstore.Store, projectID string, view governance.SourceView) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := indexing.QueueChapterCards(ctx, projectID, view)
		return err
	}
}

func pairLayer(indexing *indexingstore.Store, projectID string, view governance.SourceView) func(context.Context) error {
	return func(ctx context.Context) error { return indexing.QueueSagaLayer(ctx, projectID, view) }
}

func TestTheQueuesOfThreeVolumesBesideTheEndsOfTheirIndexesNeverDeadlock(t *testing.T) {
	ctx := context.Background()
	db, projectID, actorID, proof := newQueuePairsFixture(t, "e51")
	store := New(db)
	claims := newWorkerClaimAuthorityForTest(db)
	indexing := indexingstore.New(db)
	queue := startReadyImportQueue(t, store, projectID, actorID, proof, "review-queue-e51", 1, 2, 3)
	var cardsClaim jobs.WorkerClaim

	for position, item := range queue.Items {
		decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
		if err != nil || decision.Claim.JobID != item.ImportJobID {
			t.Fatalf("import claim %d = %+v, %v; want item %s", position+1, decision.Claim, err, item.ImportJobID)
		}
		claim := decision.Claim
		if err := store.MarkImportParsingForClaim(ctx, claim); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkImportSavingForClaim(ctx, claim, 2); err != nil {
			t.Fatal(err)
		}
		if _, err := store.FinalizeImport(ctx, library.FinalizeImportStoreInput{
			JobID: claim.JobID, ProjectID: projectID, ActorUserID: actorID,
			ExpectedClaimIncarnation: claim.ClaimIncarnation, ExpectedBookOrder: position + 1,
			StorageKey: item.StorageKey, BookTitle: fmt.Sprintf("Том %d", position+1),
			ImportMode: library.ImportModeNewBook, InitialPublication: library.InitialPublicationPublishImported,
			SourceFileName: item.SourceFileName,
			Chapters: []library.FinalizeImportChapterInput{
				{Title: "Глава 1", Paragraphs: []library.ParagraphInput{{Markdown: "Застава стояла на перевале."}}},
				{Title: "Глава 2", Paragraphs: []library.ParagraphInput{{Markdown: "Сигнальные огни горели всю ночь."}}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		indexes := map[governance.SourceView]pairIndex{}
		for range 2 {
			decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindCanonTextIndex})
			if err != nil {
				t.Fatalf("volume %d: the claim of a text index = %v", position+1, err)
			}
			var view string
			var revision uint64
			if err := db.Pool.QueryRow(ctx, `SELECT subject_json->>'sourceView', (subject_json->>'catalogRevision')::bigint FROM jobs WHERE id = $1`, decision.Claim.JobID).Scan(&view, &revision); err != nil {
				t.Fatal(err)
			}
			indexCurrentTextForTest(t, indexing, decision.Claim)
			indexes[governance.SourceView(view)] = pairIndex{claim: decision.Claim, subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: projectID, SourceView: governance.SourceView(view), CatalogRevision: revision,
			}}
		}
		draft, published := indexes[governance.SourceViewDraft], indexes[governance.SourceViewPublished]
		if draft.claim.JobID == "" || published.claim.JobID == "" {
			t.Fatalf("volume %d: the text indexes of both versions were not claimed: %+v", position+1, indexes)
		}

		switch position {
		case 0:
			// The queue of the cards of the published text holds its unit; the end of the published index waits for it with
			// the books for share; the end of the draft takes its own unit, queues and waits for the books; the queue of the draft
			// cards waits behind the end of the draft. The layer and the claim of the cards wait for nobody.
			holder := newPairTag(t, "the queue of the published cards", afterQueueLock(jobs.KindChapterCards, governance.SourceViewPublished))
			holderDone := pairRun(holder, pairQueue(indexing, projectID, governance.SourceViewPublished))
			pairPaused(t, holder, holderDone)
			publishedEnd := newPairTag(t, "the end of the published index", nil)
			publishedDone := pairRun(publishedEnd, published.end(indexing))
			pairWaits(t, db, publishedEnd, true, publishedDone)
			draftEnd := newPairTag(t, "the end of the draft index", nil)
			draftDone := pairRun(draftEnd, draft.end(indexing))
			pairWaits(t, db, draftEnd, false, draftDone)
			draftQueue := newPairTag(t, "the queue of the draft cards", nil)
			draftQueueDone := pairRun(draftQueue, pairQueue(indexing, projectID, governance.SourceViewDraft))
			pairWaits(t, db, draftQueue, true, draftQueueDone)
			layer := newPairTag(t, "the queue of the layer", nil)
			if err := pairEnded(t, layer.name, pairRun(layer, pairLayer(indexing, projectID, governance.SourceViewPublished)), 15*time.Second); err != nil {
				t.Fatalf("the queue of the layer beside the held queue of the cards = %v", err)
			}
			claimDone := make(chan error, 1)
			go func() {
				_, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
				if errors.Is(err, jobs.ErrNotFound) {
					err = nil
				}
				claimDone <- err
			}()
			if err := pairEnded(t, "the claim of the cards", claimDone, 15*time.Second); err != nil {
				t.Fatalf("the claim of the cards beside the held queue = %v", err)
			}
			holder.free()
			for name, done := range map[string]chan error{
				holder.name: holderDone, publishedEnd.name: publishedDone, draftEnd.name: draftDone, draftQueue.name: draftQueueDone,
			} {
				if err := pairEnded(t, name, done, 30*time.Second); err != nil {
					t.Fatalf("volume 1: %s = %v", name, err)
				}
			}
		case 1:
			// The end of the draft index holds its unit with the books for share; the end of the published index does not wait
			// for it; the queue of the draft cards waits; the claim of the cards takes the waiting draft Job beside it.
			holder := newPairTag(t, "the end of the draft index", afterQueueLock(jobs.KindChapterCards, governance.SourceViewDraft))
			holderDone := pairRun(holder, draft.end(indexing))
			pairPaused(t, holder, holderDone)
			publishedEnd := newPairTag(t, "the end of the published index", nil)
			if err := pairEnded(t, publishedEnd.name, pairRun(publishedEnd, published.end(indexing)), 15*time.Second); err != nil {
				t.Fatalf("volume 2: the end of the published index beside the held end of the draft = %v", err)
			}
			draftQueue := newPairTag(t, "the queue of the draft cards", nil)
			draftQueueDone := pairRun(draftQueue, pairQueue(indexing, projectID, governance.SourceViewDraft))
			pairWaits(t, db, draftQueue, true, draftQueueDone)
			claimDone := make(chan error, 1)
			go func() {
				decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
				cardsClaim = decision.Claim
				claimDone <- err
			}()
			if err := pairEnded(t, "the claim of the cards", claimDone, 15*time.Second); err != nil {
				t.Fatalf("volume 2: the claim of the cards beside the held end of the draft = %v", err)
			}
			holder.free()
			for name, done := range map[string]chan error{holder.name: holderDone, draftQueue.name: draftQueueDone} {
				if err := pairEnded(t, name, done, 30*time.Second); err != nil {
					t.Fatalf("volume 2: %s = %v", name, err)
				}
			}
		case 2:
			// The end of the published index holds its unit with the books for share; the end of the draft queues under its own
			// and waits for the books; the queues of both versions wait behind the two ends; the layer of the draft waits for
			// nobody.
			holder := newPairTag(t, "the end of the published index", afterQueueLock(jobs.KindChapterCards, governance.SourceViewPublished))
			holderDone := pairRun(holder, published.end(indexing))
			pairPaused(t, holder, holderDone)
			draftEnd := newPairTag(t, "the end of the draft index", nil)
			draftDone := pairRun(draftEnd, draft.end(indexing))
			pairWaits(t, db, draftEnd, false, draftDone)
			publishedQueue := newPairTag(t, "the queue of the published cards", nil)
			publishedQueueDone := pairRun(publishedQueue, pairQueue(indexing, projectID, governance.SourceViewPublished))
			pairWaits(t, db, publishedQueue, true, publishedQueueDone)
			draftQueue := newPairTag(t, "the queue of the draft cards", nil)
			draftQueueDone := pairRun(draftQueue, pairQueue(indexing, projectID, governance.SourceViewDraft))
			pairWaits(t, db, draftQueue, true, draftQueueDone)
			layer := newPairTag(t, "the queue of the layer", nil)
			if err := pairEnded(t, layer.name, pairRun(layer, pairLayer(indexing, projectID, governance.SourceViewDraft)), 15*time.Second); err != nil {
				t.Fatalf("volume 3: the queue of the layer beside the held end = %v", err)
			}
			holder.free()
			for name, done := range map[string]chan error{
				holder.name: holderDone, draftEnd.name: draftDone, publishedQueue.name: publishedQueueDone, draftQueue.name: draftQueueDone,
			} {
				if err := pairEnded(t, name, done, 30*time.Second); err != nil {
					t.Fatalf("volume 3: %s = %v", name, err)
				}
			}
		}
		for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
			var waiting int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND claim_incarnation = 0 AND subject_json->>'sourceView' = $2`, projectID, string(view)).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting != 1 {
				t.Fatalf("volume %d: %d cards Jobs of the %s text wait; want one", position+1, waiting, view)
			}
		}
	}
	var ready, layers int
	var queueStatus string
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM books WHERE project_id = $1 AND status = 'ready' AND deleted_at IS NULL),
		       (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'saga_layer' AND status = 'queued'),
		       (SELECT status FROM import_queues WHERE project_id = $1 AND id = $2)`, projectID, queue.ID).Scan(&ready, &layers, &queueStatus); err != nil {
		t.Fatal(err)
	}
	if ready != 3 || layers != 1 || queueStatus != string(library.ImportQueueSucceeded) {
		t.Fatalf("ready books/waiting layer Jobs/queue = %d/%d/%s; want 3/1/succeeded", ready, layers, queueStatus)
	}
	if cardsClaim.JobID == "" {
		t.Fatal("the claim of the cards beside the held end of the draft took no Job")
	}

	chapter := func(book string, number int) (string, string) {
		t.Helper()
		var bookID, chapterID string
		if err := db.Pool.QueryRow(ctx, `
			SELECT book.id::text, chapter.id::text FROM chapters AS chapter JOIN books AS book ON book.id = chapter.book_id
			WHERE book.project_id = $1 AND book.title = $2 AND chapter.deleted_at IS NULL ORDER BY chapter."order" OFFSET $3 LIMIT 1`,
			projectID, book, number-1).Scan(&bookID, &chapterID); err != nil {
			t.Fatal(err)
		}
		return bookID, chapterID
	}
	deletion := func(book string, number int) func(context.Context) error {
		bookID, chapterID := chapter(book, number)
		return func(ctx context.Context) error {
			var tombstone string
			if err := db.Pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&tombstone); err != nil {
				return err
			}
			_, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
				ActorUserID: actorID, ProjectID: projectID, BookID: bookID, ChapterID: chapterID, TombstoneID: tombstone, DeletedAt: time.Now().UTC(),
			})
			return err
		}
	}
	var publishedCardsJob string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND subject_json->>'sourceView' = 'published'`, projectID).Scan(&publishedCardsJob); err != nil {
		t.Fatal(err)
	}

	// The deletion of a chapter while the draft cards Job runs holds the project for update and queues the cards again under
	// the unit of the draft; held there, the queues, the layer and the cancel of the waiting published Job wait for the project.
	{
		holder := newPairTag(t, "the deletion of a chapter", afterQueueLock(jobs.KindChapterCards, governance.SourceViewDraft))
		holderDone := pairRun(holder, deletion("Том 1", 1))
		pairPaused(t, holder, holderDone)
		draftQueue := newPairTag(t, "the queue of the draft cards", nil)
		draftQueueDone := pairRun(draftQueue, pairQueue(indexing, projectID, governance.SourceViewDraft))
		pairWaits(t, db, draftQueue, false, draftQueueDone)
		layer := newPairTag(t, "the queue of the layer", nil)
		layerDone := pairRun(layer, pairLayer(indexing, projectID, governance.SourceViewDraft))
		pairWaits(t, db, layer, false, layerDone)
		cancel := newPairTag(t, "the cancel of the waiting published cards Job", nil)
		cancelDone := pairRun(cancel, func(ctx context.Context) error {
			_, err := jobstore.New(db).CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
				ActorUserID: actorID, ProjectID: projectID, JobID: publishedCardsJob,
				From: jobs.StatusQueued, To: jobs.StatusCanceled, UpdatedAt: time.Now().UTC(),
			})
			return err
		})
		pairWaits(t, db, cancel, false, cancelDone)
		holder.free()
		for name, done := range map[string]chan error{holder.name: holderDone, draftQueue.name: draftQueueDone, layer.name: layerDone, cancel.name: cancelDone} {
			if err := pairEnded(t, name, done, 30*time.Second); err != nil {
				t.Fatalf("the deletion held after its queue: %s = %v", name, err)
			}
		}
	}

	// The end of the stopped cards Job does not wait for a queue that holds the unit of its version.
	{
		holder := newPairTag(t, "the queue of the draft cards", afterQueueLock(jobs.KindChapterCards, governance.SourceViewDraft))
		holderDone := pairRun(holder, pairQueue(indexing, projectID, governance.SourceViewDraft))
		pairPaused(t, holder, holderDone)
		end := newPairTag(t, "the end of the stopped cards Job", nil)
		if err := pairEnded(t, end.name, pairRun(end, func(ctx context.Context) error {
			return sagamemoryrepo.New(db).FinishCards(ctx, cardsClaim, sagamemoryservice.CardsOutcome{
				Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: governance.SourceViewDraft},
				Status:  jobs.StatusCanceled,
			})
		}), 15*time.Second); err != nil {
			t.Fatalf("the end of the stopped cards Job beside the held queue = %v", err)
		}
		holder.free()
		if err := pairEnded(t, holder.name, holderDone, 30*time.Second); err != nil {
			t.Fatalf("the held queue after the end of the cards Job = %v", err)
		}
	}

	// The queue of the draft cards holds its unit; the deletion of a chapter while the draft cards Job runs waits for the
	// project, then stops the Job and queues the cards under the same unit — it reuses the Job of the queue.
	decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
	if err != nil {
		t.Fatalf("the claim of the draft cards before the second deletion = %v", err)
	}
	var claimedView string
	if err := db.Pool.QueryRow(ctx, `SELECT subject_json->>'sourceView' FROM jobs WHERE id = $1`, decision.Claim.JobID).Scan(&claimedView); err != nil || claimedView != "draft" {
		t.Fatalf("the claim before the second deletion took the %s cards Job, %v; want the draft one", claimedView, err)
	}
	{
		holder := newPairTag(t, "the queue of the draft cards", afterQueueLock(jobs.KindChapterCards, governance.SourceViewDraft))
		holderDone := pairRun(holder, pairQueue(indexing, projectID, governance.SourceViewDraft))
		pairPaused(t, holder, holderDone)
		second := newPairTag(t, "the deletion of another chapter", nil)
		secondDone := pairRun(second, deletion("Том 2", 1))
		pairWaits(t, db, second, false, secondDone)
		publishedQueue := newPairTag(t, "the queue of the published cards", nil)
		publishedQueueDone := pairRun(publishedQueue, pairQueue(indexing, projectID, governance.SourceViewPublished))
		holder.free()
		for name, done := range map[string]chan error{holder.name: holderDone, second.name: secondDone, publishedQueue.name: publishedQueueDone} {
			if err := pairEnded(t, name, done, 30*time.Second); err != nil {
				t.Fatalf("the queue held before the deletion: %s = %v", name, err)
			}
		}
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		var waiting int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND claim_incarnation = 0 AND subject_json->>'sourceView' = $2`, projectID, string(view)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting != 1 {
			t.Fatalf("after the deletions %d cards Jobs of the %s text wait; want one", waiting, view)
		}
	}
}
