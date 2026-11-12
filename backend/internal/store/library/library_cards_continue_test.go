package librarystore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/domain/quota"
	memorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	indexingrepo "canonkeeper/backend/internal/repository/postgres/indexing"
	sagarepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	indexingservice "canonkeeper/backend/internal/services/indexing"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	canonstore "canonkeeper/backend/internal/store/canon"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

// specs/2026-11-10-ux1-shelf.md UXS1.3: the author stops the preparation of a book while its chapter cards are written
// and later presses «Продолжить» — the public restart of the indexing of the same text version. The cards written
// before the stop are the same rows afterwards, and the model is called only for the chapters that had none: the
// shelf may say «продолжить», not «запустить заново».
func TestStoppedCardsContinueFromTheChaptersLeft(t *testing.T) {
	fixture := newReadyWaveFixture(t, "e71")
	ctx, db, projectID := fixture.ctx, fixture.db, fixture.projectID
	view := governance.SourceViewPublished
	// The automatic preparation of a saga is stopped by its owner or administrator.
	authorID, authorMembershipID := testUUID(71_002), testUUID(71_003)
	authorProof := insertProjectStoreUserWithSessionForTest(t, db, authorID, "continue-admin-e71@example.test")
	insertProjectStoreMembershipForTest(t, db, authorMembershipID, projectID, authorID, projects.RoleAdmin, time.Now().UTC(), false)
	fixture.load(testUUID(71_001), fixture.ownerID, "continue-book", "Книга", library.ImportModeNewBook, "",
		"Первая глава", "Вторая глава", "Третья глава", "Четвёртая глава")
	var bookID string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	// The import indexes both texts of the book. The cards of the chapters they share are written by the Job of the draft
	// text; the Job of the published text — the one the shelf shows — waits for it (specs/2026-11-11-cards-once.md CO2.2).
	for _, indexed := range []governance.SourceView{governance.SourceViewDraft, view} {
		textClaim, textSubject := claimTextIndexForTest(t, db, projectID, indexed)
		indexCurrentTextForTest(t, fixture.indexing, textClaim)
		if err := fixture.indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
			t.Fatal(err)
		}
	}
	var publishedCardsJob string
	if err := db.Pool.QueryRow(ctx, `
		SELECT id::text FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued' AND subject_json->>'sourceView' = $2
	`, projectID, string(view)).Scan(&publishedCardsJob); err != nil {
		t.Fatal(err)
	}

	// The cards Job writes two cards; while the model answers for the third chapter the author presses «Остановить»: the
	// preparation of the book stops — the Job in work and the one that waits for it.
	decision, err := fixture.claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
	if err != nil {
		t.Fatalf("cards claim: %v", err)
	}
	cardsClaim := decision.Claim
	jobsStore := jobstore.New(db)
	jobsService := jobs.NewService(jobsStore, auth.SystemClock{}, indexing.NewJobTargetAccessResolver(canonstore.New(db)))
	// The worker hears the stop at its next heartbeat and stops the handler, as the job worker does.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	model := &stoppingCardModel{stopOn: "Третья глава", stop: func() {
		for _, jobID := range []string{cardsClaim.JobID, publishedCardsJob} {
			if _, err := jobsService.CancelJob(ctx, jobs.Actor{UserID: authorID}, jobID); err != nil {
				t.Errorf("CancelJob(%s) error = %v", jobID, err)
			}
		}
		if err := jobsStore.HeartbeatWorkerClaim(ctx, cardsClaim); !errors.Is(err, jobs.ErrConflict) {
			t.Errorf("HeartbeatWorkerClaim() after the stop = %v, want the claim not extended", err)
		}
		stopRun()
	}}
	saga := sagarepo.New(db)
	builder := memoryservice.NewBuildCards(saga, saga, model, nil, "test-model", 1, nil, nil)
	draftSubject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: governance.SourceViewDraft}
	if _, err := builder.RunForClaim(runCtx, cardsClaim, draftSubject, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunForClaim() after the stop = %v, want it stopped", err)
	}
	// The worker reads the Job canceling and ends it under its claim; the cards written so far stay.
	if err := builder.CancelForClaim(ctx, cardsClaim, draftSubject); err != nil {
		t.Fatalf("CancelForClaim() error = %v", err)
	}
	assertDeletionJobStatus(t, db, cardsClaim.JobID, jobs.StatusCanceled)
	assertDeletionJobStatus(t, db, publishedCardsJob, jobs.StatusCanceled)
	subject := jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: view}
	before := publishedCardRowsForTest(t, fixture, view)
	if len(before) != 2 {
		t.Fatalf("cards before «Продолжить» = %d, want the two written before the stop", len(before))
	}

	// The shelf offers «Продолжить» only when the status names this book retryable.
	status, err := indexingrepo.New(db).GetStatus(ctx, indexingservice.GetStatusInput{
		ActorUserID: authorID, ProjectID: projectID, BookID: bookID, SourceView: indexingdomain.SourceView(view),
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Published.Context == nil || status.Published.Context.State != canon.IndexingStatePartial ||
		status.Published.Context.RetryableFailedBook == nil || status.Published.Context.RetryableFailedBook.BookID != bookID {
		t.Fatalf("status after the stop = %+v, want partial with the book to continue", status.Published.Context)
	}

	// «Продолжить»: the public restart of the indexing of the same version.
	if _, err := indexing.NewService(fixture.indexing, indexing.NewSecureIDGenerator(), auth.SystemClock{}).StartIndexing(ctx,
		indexing.Actor{UserID: authorID}, indexing.StartIndexingInput{
			ProjectID: projectID, SourceView: view, IdempotencyKey: "continue-the-cards",
			RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/indexing", SessionWriteProof: authorProof,
		}); err != nil {
		t.Fatalf("StartIndexing() error = %v", err)
	}
	textClaim, textSubject := claimTextIndexForTest(t, db, projectID, view)
	indexCurrentTextForTest(t, fixture.indexing, textClaim)
	if err := fixture.indexing.CompleteCanonTextIndexForClaim(ctx, textClaim, textSubject); err != nil {
		t.Fatal(err)
	}
	counting := &stoppingCardModel{}
	continued, err := fixture.claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterCards})
	if err != nil {
		t.Fatalf("cards claim after «Продолжить»: %v", err)
	}
	if status, err := memoryservice.NewBuildCards(saga, saga, counting, nil, "test-model", 1, nil, nil).
		RunForClaim(ctx, continued.Claim, subject, true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("cards after «Продолжить» = %q, %v; want succeeded", status, err)
	}
	if asked := counting.chaptersAsked(); len(asked) != 2 || !asked["Третья глава"] || !asked["Четвёртая глава"] {
		t.Fatalf("the model was asked after «Продолжить» about %v, want only the two chapters left", asked)
	}
	after := publishedCardRowsForTest(t, fixture, view)
	if len(after) != 4 {
		t.Fatalf("cards after «Продолжить» = %d, want a card of every chapter", len(after))
	}
	for key, created := range before {
		if !after[key].Equal(created) {
			t.Fatalf("the card %s was written again: %v, then %v", key, created, after[key])
		}
	}
	status, err = indexingrepo.New(db).GetStatus(ctx, indexingservice.GetStatusInput{
		ActorUserID: authorID, ProjectID: projectID, BookID: bookID, SourceView: indexingdomain.SourceView(view),
	})
	if err != nil || status.Published.Context == nil || status.Published.Context.State != canon.IndexingStateReady {
		t.Fatalf("status after «Продолжить» = %+v, %v; want the book ready", status.Published.Context, err)
	}
}

// publishedCardRowsForTest are the card rows of the chapters of a version, each with the moment it was written.
func publishedCardRowsForTest(t *testing.T, fixture *readyWaveFixture, view governance.SourceView) map[string]time.Time {
	t.Helper()
	rows, err := fixture.db.Pool.Query(fixture.ctx, `
		SELECT card.chapter_id::text || ':' || encode(card.content_hash, 'hex'), card.created_at
		FROM chapter_cards AS card
		JOIN chapter_source_members AS member
		  ON member.chapter_id = card.chapter_id AND member.project_id = card.project_id
		WHERE card.project_id = $1 AND member.source_view = $2
	`, fixture.projectID, string(view))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cards := map[string]time.Time{}
	for rows.Next() {
		var key string
		var created time.Time
		if err := rows.Scan(&key, &created); err != nil {
			t.Fatal(err)
		}
		cards[key] = created
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cards
}

// stoppingCardModel writes a card of every chapter and remembers which chapters it was asked about; when it is first
// asked about the chapter stopOn it runs stop first, as the author who presses «Остановить» while the model answers.
type stoppingCardModel struct {
	mu      sync.Mutex
	asked   map[string]bool
	stopOn  string
	stopped bool
	stop    func()
}

var continueChapterTexts = []string{"Первая глава", "Вторая глава", "Третья глава", "Четвёртая глава"}

func (model *stoppingCardModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions == memorydomain.NameInstructions {
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	model.mu.Lock()
	if model.asked == nil {
		model.asked = map[string]bool{}
	}
	for _, text := range continueChapterTexts {
		if strings.Contains(input, text) {
			model.asked[text] = true
		}
	}
	stopNow := model.stop != nil && !model.stopped && model.stopOn != "" && strings.Contains(input, model.stopOn)
	if stopNow {
		model.stopped = true
	}
	model.mu.Unlock()
	if stopNow {
		model.stop()
	}
	return `{"summary": "Пересказ главы"}`, quota.ModelUsage{Calls: 1}, nil
}

func (model *stoppingCardModel) chaptersAsked() map[string]bool {
	model.mu.Lock()
	defer model.mu.Unlock()
	asked := make(map[string]bool, len(model.asked))
	for text := range model.asked {
		asked[text] = true
	}
	return asked
}
