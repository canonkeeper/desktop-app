package sagamemory

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

// namesCardModel writes cards naming the hero differently in each chapter and answers the registry
// call; during the call runs what a test wants to happen meanwhile.
type namesCardModel struct {
	countingCardModel
	registryCalls int
	during        func()
}

func (model *namesCardModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions != domain.NameInstructions {
		output, spent, err := model.countingCardModel.Complete(ctx, instructions, input)
		if err == nil && output != `{}` {
			output = `{"summary": "Пересказ", "characters": [{"name": "Мирон", "aliases": ["Ворон"], "p": 1}, {"name": "Ворон", "p": 1}]}`
		}
		return output, spent, err
	}
	model.registryCalls++
	if model.during != nil {
		model.during()
	}
	return `{"persons":[{"name":"мирон","names":["ворон"],"roles":["первый король"]}],"shared":[{"name":"король","who":["мирон"]}],"wrong":[]}`, quota.ModelUsage{Calls: 1}, nil
}

func namesRowForTest(t *testing.T, db *storedb.DB) (rows int, model string) {
	t.Helper()
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT count(*), coalesce(max(model), '') FROM saga_names WHERE project_id = $1
	`, memoryProjectID).Scan(&rows, &model); err != nil {
		t.Fatal(err)
	}
	return rows, model
}

// specs/2026-11-01-index-foundation.md R5.1, R5.2, R5.4: the Job that carded the last chapter builds
// the registry, the saga reads it, and a later Job with the same names does not call the model.
func TestCardsJobSavesTheRegistryTheSagaReads(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_names_job")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	if registry, err := repository.Names(ctx, memoryProjectID); err != nil || registry != nil {
		t.Fatalf("Names() before = %+v, %v; want none", registry, err)
	}
	model := &namesCardModel{}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, nil, nil)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if rows, used := namesRowForTest(t, db); model.registryCalls != 1 || rows != 1 || used != "test-model" {
		t.Fatalf("%d registry calls, %d rows of model %q", model.registryCalls, rows, used)
	}
	saga := service.NewSaga(repository, repository)
	outline, err := saga.Outline(ctx, memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	set, err := saga.Cards(ctx, outline, 0, -1, domain.CardParts{Mentions: true})
	if err != nil || set.Names == nil {
		t.Fatalf("card set = %+v, %v; want the registry", set, err)
	}
	hero, ok := set.Glossary.Lookup("первый король")
	if !ok || hero.Name != "Мирон" || len(hero.Chapters) != 3 || len(set.Glossary.Entries) != 1 {
		t.Fatalf("hero by role = %+v, %v; entries %d", hero, ok, len(set.Glossary.Entries))
	}
	if without, err := saga.Cards(ctx, outline, 0, -1, domain.CardParts{Summary: true}); err != nil || without.Names != nil {
		t.Fatalf("a read without mentions = %+v, %v; it needs no registry", without, err)
	}

	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded || model.registryCalls != 1 {
		t.Fatalf("second Job = %q, %v, %d registry calls; want no new call", status, err, model.registryCalls)
	}
}

// R5.3: a registry is saved only under the running claim and only while every chapter text of its
// list is current: a chapter deleted during the long call leaves no names of it behind.
func TestSaveNamesRefusesALostClaimAndAGoneText(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_names_fence")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	claim := claimCardsJobForTest(t, db)
	key := func(chapter int) service.CardKey {
		var stored []byte
		if err := db.Pool.QueryRow(ctx, `SELECT chapter_content_hash FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[chapter].id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return service.CardKey{ChapterID: memoryChapters[chapter].id, ContentHash: hex.EncodeToString(stored)}
	}
	names := service.StoredNames{Keys: []service.CardKey{key(0), key(1), key(2)}, Digest: strings.Repeat("a", 64), Model: "test-model"}

	lost := claim
	lost.ClaimIncarnation++
	if err := repository.SaveNames(ctx, lost, names); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("SaveNames under a lost claim = %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[1].id); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveNames(ctx, claim, names); !errors.Is(err, domain.ErrTextChanged) {
		t.Fatalf("SaveNames with a gone text = %v", err)
	}
	if rows, _ := namesRowForTest(t, db); rows != 0 {
		t.Fatalf("rows = %d, want none", rows)
	}
	names.Keys = []service.CardKey{key(0), key(2)}
	if err := repository.SaveNames(ctx, claim, names); err != nil {
		t.Fatal(err)
	}
	names.Digest, names.Model = strings.Repeat("b", 64), "next-model"
	if err := repository.SaveNames(ctx, claim, names); err != nil {
		t.Fatal(err)
	}
	if rows, used := namesRowForTest(t, db); rows != 1 || used != "next-model" {
		t.Fatalf("rows = %d of %q, want one row replaced", rows, used)
	}
	if digest, err := repository.NamesDigest(ctx, memoryProjectID); err != nil || digest != names.Digest {
		t.Fatalf("NamesDigest() = %q, %v", digest, err)
	}
}

// R5.3 through the Job: the source goes while the model answers; the Job ends well without a row.
func TestChapterGoneDuringTheCallLeavesNoRegistry(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_names_gone")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	var failed []string
	model := &namesCardModel{during: func() {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[2].id); err != nil {
			t.Error(err)
		}
	}}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 1, func(_, stage string, _ error) {
		failed = append(failed, stage)
	}, nil)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if rows, _ := namesRowForTest(t, db); rows != 0 || len(failed) != 1 || failed[0] != "store" {
		t.Fatalf("rows = %d, failures %v; want no row and a logged failure", rows, failed)
	}
}

// R5.6: the start queues a carded version without a registry; its Job builds the registry and no
// card, and the next start queues nothing.
func TestStartQueuesAVersionWithoutARegistry(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_names_start")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	model := &namesCardModel{}
	build := mapCardsJob(t, repository, model)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	cards := model.count()
	if _, err := db.Pool.Exec(ctx, `DELETE FROM saga_names WHERE project_id = $1`, memoryProjectID); err != nil {
		t.Fatal(err)
	}
	catchUp := service.NewQueueMissingCards(repository, repository, repository, repository)
	if queued, err := catchUp.Execute(ctx); err != nil || queued != 1 {
		t.Fatalf("start without a registry = %d, %v", queued, err)
	}
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if rows, _ := namesRowForTest(t, db); rows != 1 || model.count() != cards || model.registryCalls != 2 {
		t.Fatalf("rows = %d, cards built %d, registry calls %d; want the registry back and no card built", rows, model.count()-cards, model.registryCalls)
	}
	if queued, err := catchUp.Execute(ctx); err != nil || queued != 0 {
		t.Fatalf("next start = %d, %v; want nothing", queued, err)
	}
}

// R5.6: a load queues the Job of the project's versions; a Job waiting is reused.
func TestQueueLoadedCardsQueuesTheVersions(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_names_load")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	for run := 1; run <= 2; run++ {
		if err := repository.QueueLoadedCards(ctx, memoryProjectID); err != nil {
			t.Fatal(err)
		}
	}
	var waiting int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued'`, memoryProjectID).Scan(&waiting); err != nil || waiting != 1 {
		t.Fatalf("queued = %d, %v; want one", waiting, err)
	}
}
