package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

// operatorCardsForTest are cards of every current chapter text of the memory project, as a
// chapter-cards build writes them.
func operatorCardsForTest(t *testing.T, repository *Repository, model string, version int) []OperatorCard {
	t.Helper()
	selection, err := repository.Selection(context.Background(), memoryProjectID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	cards := make([]OperatorCard, len(selection.Chapters))
	for i, chapter := range selection.Chapters {
		body, _ := json.Marshal(domain.Card{Summary: "Карточка " + model + ": " + chapter.Title})
		cards[i] = OperatorCard{
			ProjectID: memoryProjectID, ChapterID: chapter.ID, ContentHash: chapter.ContentHash, Version: version, Model: model, Card: body,
		}
	}
	return cards
}

func storedCardsForTest(t *testing.T, db *storedb.DB) map[string]string {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `SELECT chapter_id::text, model || ' v' || version || ' ' || (card_json->>'summary') FROM chapter_cards WHERE project_id=$1`, memoryProjectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := map[string]string{}
	for rows.Next() {
		var chapter, card string
		if err := rows.Scan(&chapter, &card); err != nil {
			t.Fatal(err)
		}
		stored[chapter] = card
	}
	return stored
}

// UC1 load (specs/2026-10-29-chapter-cards-quality.md): cards built elsewhere replace the cards of
// the same texts whatever model wrote them, in one transaction per project; a project is refused
// whole when a card is of other instructions, its text is no longer current, it does not check as
// a card, or a cards Job of the project runs.
func TestLoadCardsReplacesCurrentTextsAndRefusesWholeProject(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_operator_cards")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)

	luna := operatorCardsForTest(t, repository, "gpt-6-luna", domain.CardVersion)
	if outcome, err := repository.LoadCards(ctx, memoryProjectID, luna[:1]); err != nil || outcome != (CardsLoad{Inserted: 1}) {
		t.Fatalf("first load = %+v, %v; want one card inserted", outcome, err)
	}
	sol := operatorCardsForTest(t, repository, "gpt-6.1-sol", domain.CardVersion)
	if outcome, err := repository.LoadCards(ctx, memoryProjectID, sol); err != nil || outcome != (CardsLoad{Inserted: 2, Replaced: 1}) {
		t.Fatalf("second load = %+v, %v; want the luna card replaced and two inserted", outcome, err)
	}
	before := storedCardsForTest(t, db)
	for _, card := range before {
		if !strings.HasPrefix(card, fmt.Sprintf("gpt-6.1-sol v%d Карточка gpt-6.1-sol", domain.CardVersion)) {
			t.Fatalf("stored cards = %v, want every card of the sol load", before)
		}
	}

	refusals := map[string]func([]OperatorCard) []OperatorCard{
		"of instructions v5": func(cards []OperatorCard) []OperatorCard {
			cards[1].Version = domain.CardVersion + 1
			return cards
		},
		"changed since the snapshot": func(cards []OperatorCard) []OperatorCard {
			stale := make([]byte, 32)
			cards[2].ContentHash = hex.EncodeToString(stale)
			return cards
		},
		"does not check as a card": func(cards []OperatorCard) []OperatorCard {
			cards[0].Card = json.RawMessage(`{"summary": ""}`)
			return cards
		},
		"belongs to project": func(cards []OperatorCard) []OperatorCard {
			cards[0].ProjectID = memoryOwnerID
			return cards
		},
	}
	for reason, spoil := range refusals {
		cards := spoil(operatorCardsForTest(t, repository, "other-model", domain.CardVersion))
		outcome, err := repository.LoadCards(ctx, memoryProjectID, cards)
		if err != nil || !strings.Contains(outcome.Refused, reason) || outcome.Inserted+outcome.Replaced != 0 {
			t.Fatalf("load with a card %q = %+v, %v; want the project refused", reason, outcome, err)
		}
		if after := storedCardsForTest(t, db); len(after) != len(before) || after[memoryChapters[1].id] != before[memoryChapters[1].id] {
			t.Fatalf("a refused load wrote cards: %v, was %v", after, before)
		}
	}

	claim := claimCardsJobForTest(t, db)
	outcome, err := repository.LoadCards(ctx, memoryProjectID, operatorCardsForTest(t, repository, "other-model", domain.CardVersion))
	if err != nil || !strings.Contains(outcome.Refused, "cards Job of the project is running") {
		t.Fatalf("load during a cards Job = %+v, %v; want it refused", outcome, err)
	}
	model := &countingCardModel{}
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 1, nil, nil)
	if status, err := build.RunForClaim(ctx, claim, cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded || model.count() != 0 {
		t.Fatalf("cards Job after loads = %q, %v, %d model calls; want it done without the model", status, err, model.count())
	}
	if after := storedCardsForTest(t, db); len(after) != 3 || after[memoryChapters[0].id] != before[memoryChapters[0].id] {
		t.Fatalf("the cards Job rebuilt loaded cards of the current instructions: %v", after)
	}
}

// specs/2026-10-29-chapter-cards-quality.md R4.2: the facts of one entry are read inside
// PostgreSQL — subjects compared without case, ё and stress marks — and only for the asked texts.
func TestFactsReadsTheFactsOfTheNamesOfCurrentTexts(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_facts")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	cards := operatorCardsForTest(t, repository, "gpt-6.1-sol", domain.CardVersion)
	for i := range cards {
		body, _ := json.Marshal(domain.Card{Summary: "Пересказ", Facts: []domain.Fact{
			{Subject: "ВОРО́Н", Attribute: "уровень", Value: fmt.Sprint(10 + i), P: 1},
			{Subject: "Любава", Attribute: "возраст", Value: "15 лет", P: 2},
			{Subject: "Пётр", Attribute: "класс", Value: "маг", P: 3},
			{Subject: "Белый\u00a0 Ворон", Attribute: "титул", Value: "посланник", P: 4},
		}, Knows: []domain.Knowledge{
			{Who: "Ворон", Kind: "скрывает", What: "своё имя", P: 5},
			{Who: "Любава", What: "ничего о брате", P: 6},
		}})
		cards[i].Card = body
	}
	if outcome, err := repository.LoadCards(ctx, memoryProjectID, cards); err != nil || outcome.Inserted != 3 {
		t.Fatalf("load = %+v, %v", outcome, err)
	}
	keys := make([]service.CardKey, 0, 2)
	for _, card := range cards[:2] {
		keys = append(keys, service.CardKey{ChapterID: card.ChapterID, ContentHash: card.ContentHash})
	}
	titled, err := repository.Facts(ctx, memoryProjectID, keys[:1], []string{"белый ворон"})
	if err != nil || len(titled[keys[0]]) != 1 || titled[keys[0]][0].Value != "посланник" {
		t.Fatalf("facts of «белый ворон» = %+v, %v; want the title written with a no-break space", titled, err)
	}
	unnamed, err := repository.Facts(ctx, memoryProjectID, keys[:1], []string{"любава"})
	if err != nil || len(unnamed[keys[0]]) != 2 || unnamed[keys[0]][1].Attribute != "знание" {
		t.Fatalf("facts of «любава» = %+v, %v; want her age and the knowledge without a kind", unnamed, err)
	}
	facts, err := repository.Facts(ctx, memoryProjectID, keys, []string{"ворон", "петр", "белый ворон"})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts of %d texts, want the two asked", len(facts))
	}
	for key, found := range facts {
		if len(found) != 4 || found[0].Attribute != "уровень" || found[1].Value != "маг" || found[2].Value != "посланник" {
			t.Fatalf("facts of %s = %+v, want the hero's level, Пётр's class and the title under a name with a no-break space", key.ChapterID, found)
		}
		// specs/2026-11-01-index-foundation.md R4.1: what a v4 card says a character knows comes after the facts.
		if known := found[3]; known.Attribute != "скрывает" || known.Value != "своё имя" || known.P != 5 {
			t.Fatalf("knowledge of %s = %+v", key.ChapterID, known)
		}
	}
}

// specs/2026-10-29-chapter-cards-quality.md R1.6: status covers the versions the cards Job builds
// (IndexedVersions): a project nobody active can open is left out.
func TestCardsCoverageLeavesOutProjectsWithoutAnActiveOwner(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_coverage")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	if coverage, err := repository.CardsCoverage(ctx); err != nil || len(coverage) != 1 || coverage[0].Texts != 3 {
		t.Fatalf("coverage = %+v, %v; want the project's three texts", coverage, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1`, memoryOwnerID); err != nil {
		t.Fatal(err)
	}
	if coverage, err := repository.CardsCoverage(ctx); err != nil || len(coverage) != 0 {
		t.Fatalf("coverage with a disabled owner = %+v, %v; want none", coverage, err)
	}
}

// R1.5: a load holds the project's queued cards Job until it commits — the claim skips locked
// Job rows — so the Job never starts building cards the load is writing.
func TestLoadCardsHoldsTheQueuedCardsJob(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_load_lock")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	claim := claimCardsJobForTest(t, db)
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET status='queued', claim_incarnation=2, claimed_at=NULL WHERE id=$1`, claim.JobID); err != nil {
		t.Fatal(err)
	}
	project, _ := storedb.ParseUUID(memoryProjectID)
	cards := operatorCardsForTest(t, repository, "gpt-6.1-sol", domain.CardVersion)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var outcome CardsLoad
	if err := loadCardsInTx(ctx, tx, project, memoryProjectID, cards, &outcome); err != nil {
		t.Fatalf("load = %v (%+v)", err, outcome)
	}
	var skipped int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM jobs WHERE id=$1 FOR UPDATE SKIP LOCKED) AS claimable`, claim.JobID).Scan(&skipped); err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatal("the queued cards Job could be claimed while the load was writing")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if outcome.Inserted != 3 {
		t.Fatalf("load inserted %d cards, want 3", outcome.Inserted)
	}
}

// R1.5: the model of a loaded card is stored without the spaces around it.
func TestLoadCardsTrimsTheModel(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_cards_model")
	seedMemoryProject(t, db)
	repository := New(db)
	cards := operatorCardsForTest(t, repository, " gpt-6.1-sol ", domain.CardVersion)
	if outcome, err := repository.LoadCards(context.Background(), memoryProjectID, cards); err != nil || outcome.Inserted != 3 {
		t.Fatalf("load = %+v, %v", outcome, err)
	}
	for _, card := range storedCardsForTest(t, db) {
		if !strings.HasPrefix(card, "gpt-6.1-sol v") {
			t.Fatalf("stored card %q, want the trimmed model", card)
		}
	}
}
