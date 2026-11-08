package sagamemory

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// R2.2, R2.3: a cards Job builds one card per chapter text, a later Job rebuilds only the
// chapter whose text changed, and the card of the replaced text is deleted at the end.
func TestCardsJobReusesCardsByTextAndDeletesStaleOnes(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	model := &countingCardModel{}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, nil, nil)

	first := claimCardsJobForTest(t, db)
	status, err := build.RunForClaim(ctx, first, cardsSubjectForTest(), true)
	if err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("first run = %q, %v", status, err)
	}
	if model.count() != 3 || countCardsForTest(t, db) != 3 {
		t.Fatalf("first run: %d model calls, %d cards; want 3 and 3", model.count(), countCardsForTest(t, db))
	}
	var resultJSON []byte
	var jobStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status, result_json FROM jobs WHERE id=$1`, first.JobID).Scan(&jobStatus, &resultJSON); err != nil {
		t.Fatal(err)
	}
	result, err := jobs.DecodeResult(jobs.KindChapterCards, resultJSON)
	want := jobs.ChapterCardsResult{Type: jobs.KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 3, Built: 3}
	if err != nil || jobStatus != "succeeded" || result != want {
		t.Fatalf("first Job = %s %#v, %v; want succeeded %#v", jobStatus, result, err, want)
	}

	changed := sha256.Sum256([]byte("new text of chapter 2"))
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_text_index_state SET chapter_content_hash=$2 WHERE chapter_id=$1`, memoryChapters[2].id, changed[:]); err != nil {
		t.Fatal(err)
	}
	second := claimCardsJobForTest(t, db)
	if status, err := build.RunForClaim(ctx, second, cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("second run = %q, %v", status, err)
	}
	if model.count() != 4 {
		t.Fatalf("model calls after a text change = %d, want one more", model.count())
	}
	var cards, currentCards int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE chapter_id=$1 AND content_hash=$2)
		FROM chapter_cards WHERE project_id=$3
	`, memoryChapters[2].id, changed[:], memoryProjectID).Scan(&cards, &currentCards); err != nil {
		t.Fatal(err)
	}
	if cards != 3 || currentCards != 1 {
		t.Fatalf("cards after rebuild = %d (current text %d), want the stale card replaced", cards, currentCards)
	}

	saga := service.NewSaga(repository, repository)
	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	set, err := saga.Cards(ctx, outline, 0, -1, domain.AllCardParts)
	if err != nil || len(set.Cards) != 3 || !strings.HasPrefix(set.Cards[1].Summary, "Пересказ") {
		t.Fatalf("card set = %+v, %v; want the three current cards", set, err)
	}
}

// specs/2026-11-01-index-foundation.md R3.1: a card written by older instructions keeps serving the
// chat, and the cards Job does not rebuild it: which sagas get the new instructions is the operator's choice.
func TestCardsJobKeepsCardsOfOlderInstructions(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_card_version")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	model := &countingCardModel{}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, nil, nil)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("first run = %q, %v", status, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_cards SET version = 1, card_json = '{"summary":"Старая карточка"}' WHERE chapter_id = $1`, memoryChapters[1].id); err != nil {
		t.Fatal(err)
	}
	saga := service.NewSaga(repository, repository)
	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if set, err := saga.Cards(ctx, outline, 0, -1, domain.AllCardParts); err != nil || len(set.Cards) != 3 {
		t.Fatalf("cards = %+v, %v; want the old card served", set, err)
	}
	calls := model.count()
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("second run = %q, %v", status, err)
	}
	var version int
	if err := db.Pool.QueryRow(ctx, `SELECT version FROM chapter_cards WHERE chapter_id = $1`, memoryChapters[1].id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if model.count() != calls || version != 1 {
		t.Fatalf("second run = %d model calls, version %d; want none and the old card kept", model.count()-calls, version)
	}
}

// A card of newer instructions, left by a release rolled back, reads as a superset and counts as
// present: the Job of the older release neither rebuilds it nor loops over it on every start.
func TestCardsJobKeepsCardsOfNewerInstructions(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_card_newer")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	model := &countingCardModel{}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, nil, nil)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("first run = %q, %v", status, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE chapter_cards SET version = $2, card_json = card_json || '{"facts":[{"subject":"Герой","attribute":"возраст","value":"24","p":1}]}' WHERE chapter_id = $1`,
		memoryChapters[1].id, domain.CardVersion+1); err != nil {
		t.Fatal(err)
	}
	calls := model.count()
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("second run = %q, %v", status, err)
	}
	var version int
	if err := db.Pool.QueryRow(ctx, `SELECT version FROM chapter_cards WHERE chapter_id = $1`, memoryChapters[1].id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if model.count() != calls || version != domain.CardVersion+1 {
		t.Fatalf("newer card = %d model calls, version %d; want none and the newer card kept", model.count()-calls, version)
	}
	saga := service.NewSaga(repository, repository)
	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if set, err := saga.Cards(ctx, outline, 0, -1, domain.AllCardParts); err != nil || len(set.Cards) != 3 {
		t.Fatalf("cards with a newer card = %+v, %v; want all three served", set, err)
	}
}

// specs/2026-11-04-saga-map.md R1.5: the revision of the cards changes when a cards Job writes cards
// and stays while nothing is written.
func TestCardsRevisionFollowsTheCards(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_revision")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	saga := service.NewSaga(repository, repository)
	revision := func() string {
		t.Helper()
		now, err := saga.CurrentMap(ctx, memoryProjectID, "draft")
		if err != nil || len(now.Revision) != 32 {
			t.Fatalf("revision = %+v, %v", now, err)
		}
		return now.Revision
	}
	empty := revision()
	build := service.NewBuildCards(repository, repository, &countingCardModel{}, nil, "test-model", 2, nil, nil)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("cards run = %q, %v", status, err)
	}
	built := revision()
	if built == empty || revision() != built {
		t.Fatalf("revision before %s, after the cards %s: want a new one that stays", empty, built)
	}
	stamp, err := repository.CardsStamp(ctx, memoryProjectID, []service.CardKey{{ChapterID: memoryChapters[1].id, ContentHash: "00"}})
	if err != nil || stamp.Cards != 0 {
		t.Fatalf("stamp of a text without a card = %+v, %v", stamp, err)
	}
}

func cardsSubjectForTest() jobs.ChapterCardsSubject {
	return jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: memoryProjectID, SourceView: governance.SourceViewDraft}
}

func claimCardsJobForTest(t *testing.T, db *storedb.DB) jobs.WorkerClaim {
	t.Helper()
	ctx := context.Background()
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{ProjectID: memoryProjectID, ProjectGeneration: 1}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		job, err = jobstore.New(db).UpsertChapterCardsIntent(ctx, tx, jobs.ChapterCardsIntentInput{
			Subject: cardsSubjectForTest(), Source: jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
			CreatedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.StatusQueued || job.QueueClass != jobs.QueueClassAutomatic || job.CreatorUserID != "" {
		t.Fatalf("cards intent = %+v, want a queued automatic Job without a creator", job)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='running', claim_incarnation=1, claimed_at=clock_timestamp() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: memoryProjectID, JobID: job.ID, ClaimIncarnation: 1}
}

func countCardsForTest(t *testing.T, db *storedb.DB) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM chapter_cards WHERE project_id=$1`, memoryProjectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type countingCardModel struct {
	mu    sync.Mutex
	calls int
}

func (model *countingCardModel) Complete(_ context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions == domain.NameInstructions {
		// The registry call: the tests of cards count cards.
		return `{"persons":[]}`, quota.ModelUsage{Calls: 1}, nil
	}
	if strings.Contains(input, "КАРТОЧКА:") {
		// The pass over a written card adds nothing here; the tests count cards.
		return `{}`, quota.ModelUsage{Calls: 1}, nil
	}
	model.mu.Lock()
	model.calls++
	model.mu.Unlock()
	return `{"summary": "Пересказ главы", "characters": [{"name": "Мирон", "p": 1}]}`, quota.ModelUsage{Calls: 1}, nil
}

func (model *countingCardModel) count() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls
}
